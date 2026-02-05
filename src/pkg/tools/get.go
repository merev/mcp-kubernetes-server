package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discovery "k8s.io/client-go/discovery"
)

// ---- get.py port ----

// K8sGet matches Python k8s_get(resource, name, namespace):
// - resource can match plural name, singularName, or shortNames
// - name="" means list
// - namespace="" means all namespaces (for namespaced resources)
// - for namespaced GET with no namespace specified, default "default"
func K8sGet(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	resource, _ := args["resource"].(string)
	name, _ := args["name"].(string)

	// namespace may come as string or may be missing
	namespace, _ := args["namespace"].(string)

	if strings.TrimSpace(resource) == "" {
		return textErrorResult("resource is required"), nil, nil
	}

	disc, err := getDiscovery()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}
	dyn, err := getDynamic()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	gvr, namespaced, found := findGVR(disc, resource)
	if !found {
		return textErrorResult(fmt.Sprintf("Error: resource '%s' not found in cluster", resource)), nil, nil
	}

	ri := dyn.Resource(gvr)

	// Mirror Python behavior
	if namespaced {
		if name != "" {
			ns := namespace
			if ns == "" {
				ns = "default"
			}
			obj, err := ri.Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return textErrorResult(formatK8sErr(err)), nil, nil
			}
			return marshalUnstructured(obj), nil, nil
		}

		// list
		if namespace == "" {
			// all namespaces
			list, err := ri.Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
			if err != nil {
				return textErrorResult(formatK8sErr(err)), nil, nil
			}
			return marshalUnstructured(list), nil, nil
		}

		list, err := ri.Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		return marshalUnstructured(list), nil, nil
	}

	// cluster-scoped resources
	if name != "" {
		obj, err := ri.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		return marshalUnstructured(obj), nil, nil
	}

	list, err := ri.List(ctx, metav1.ListOptions{})
	if err != nil {
		return textErrorResult(formatK8sErr(err)), nil, nil
	}
	return marshalUnstructured(list), nil, nil
}

// K8sApis: list APIs similar in spirit to Python k8s_apis().
// Python returns /api versions via ApisApi().get_api_versions().
// In Go we return discovery groups + resources (more complete, and useful).
func K8sApis(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	disc, err := getDiscovery()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	groups, resources, err := disc.ServerGroupsAndResources()
	// This can fail partially due to auth; still return what we can.
	partial := ""
	if err != nil {
		partial = err.Error()
	}

	out := map[string]any{
		"groups":    groups,
		"resources": resources,
	}
	if partial != "" {
		out["warning"] = "partial discovery failure: " + partial
	}

	b, _ := json.MarshalIndent(out, "", "  ")
	return textOKResult(string(b)), nil, nil
}

// K8sCrds: list CRDs like Python k8s_crds().
func K8sCrds(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	ext, err := getAPIExtensions()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	crds, err := ext.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
	if err != nil {
		return textErrorResult(formatK8sErr(err)), nil, nil
	}

	b, _ := json.MarshalIndent(crds, "", "  ")
	return textOKResult(string(b)), nil, nil
}

// ---- helpers ----

func marshalUnstructured(obj interface{}) *mcp.CallToolResult {
	b, _ := json.MarshalIndent(obj, "", "  ")
	return textOKResult(string(b))
}

func formatK8sErr(err error) string {
	if apierrors.IsNotFound(err) {
		return "Error:\nNotFound: " + err.Error()
	}
	if apierrors.IsForbidden(err) {
		return "Error:\nForbidden: " + err.Error()
	}
	if apierrors.IsUnauthorized(err) {
		return "Error:\nUnauthorized: " + err.Error()
	}
	return "Error:\n" + err.Error()
}

func findGVR(disc discovery.DiscoveryInterface, target string) (schema.GroupVersionResource, bool, bool) {
	target = strings.TrimSpace(target)

	// Try preferred resources first
	lists, err := disc.ServerPreferredResources()
	if err != nil {
		// If partial discovery fails, lists may still be usable; keep going if not nil.
	}

	for _, rl := range lists {
		gv, parseErr := schema.ParseGroupVersion(rl.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, r := range rl.APIResources {
			if matchResource(r, target) {
				return schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: r.Name, // plural name used in the URL
				}, r.Namespaced, true
			}
		}
	}

	// Fallback: full groups+resources discovery (may be heavy)
	_, resources, _ := disc.ServerGroupsAndResources()
	for _, rl := range resources {
		gv, parseErr := schema.ParseGroupVersion(rl.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, r := range rl.APIResources {
			if matchResource(r, target) {
				return schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: r.Name,
				}, r.Namespaced, true
			}
		}
	}

	return schema.GroupVersionResource{}, false, false
}

func matchResource(res metav1.APIResource, target string) bool {
	if target == res.Name {
		return true
	}
	if target == res.SingularName && res.SingularName != "" {
		return true
	}
	for _, sn := range res.ShortNames {
		if target == sn {
			return true
		}
	}
	return false
}

// Ensure unstructured types get marshaled cleanly (they do) and keep unused import away:
var _ = unstructured.Unstructured{}
