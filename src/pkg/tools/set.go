package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// K8sSetResources ports k8s_set_resources(...)
func K8sSetResources(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	resourceType, _ := args["resource_type"].(string)
	resourceName, _ := args["resource_name"].(string)
	namespace, _ := args["namespace"].(string)

	if strings.TrimSpace(resourceType) == "" {
		return textErrorResult("resource_type is required"), nil, nil
	}
	if strings.TrimSpace(resourceName) == "" {
		return textErrorResult("resource_name is required"), nil, nil
	}
	if namespace == "" {
		namespace = "default"
	}

	containers := stringSliceFromArgs(args, "containers")

	limits, _ := args["limits"].(map[string]any)
	requests, _ := args["requests"].(map[string]any)

	disc, err := getDiscovery()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}
	dyn, err := getDynamic()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	gvr, namespaced, found := findGVR(disc, resourceType)
	if !found {
		// Python also tries resource_type + "s"
		gvr, namespaced, found = findGVR(disc, resourceType+"s")
	}
	if !found {
		return textErrorResult(fmt.Sprintf("Error: resource '%s' not found in cluster", resourceType)), nil, nil
	}

	ri := dyn.Resource(gvr)
	var obj *unstructured.Unstructured
	if namespaced {
		o, err := ri.Namespace(namespace).Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		obj = o
	} else {
		o, err := ri.Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		obj = o
	}

	kind := strings.ToLower(obj.GetKind())
	if kind == "" {
		kind = strings.ToLower(resourceType)
	}

	// Modify containers depending on object kind (python branches by resource_type, but kind is safer)
	var containersPath []string
	switch kind {
	case "deployment", "statefulset", "daemonset", "replicaset":
		containersPath = []string{"spec", "template", "spec", "containers"}
	case "pod":
		containersPath = []string{"spec", "containers"}
	default:
		// fallback on requested resourceType just like python
		switch strings.ToLower(resourceType) {
		case "deployment", "statefulset", "daemonset", "replicaset":
			containersPath = []string{"spec", "template", "spec", "containers"}
		case "pod":
			containersPath = []string{"spec", "containers"}
		default:
			return textErrorResult(fmt.Sprintf("Error: resource type '%s' does not support setting resources", resourceType)), nil, nil
		}
	}

	if err := updateContainers(obj.Object, containersPath, func(c map[string]any) error {
		if len(containers) > 0 {
			if !stringInSlice(fmtAny(c["name"]), containers) {
				return nil
			}
		}

		res, _ := c["resources"].(map[string]any)
		if res == nil {
			res = map[string]any{}
			c["resources"] = res
		}

		if limits != nil {
			res["limits"] = limits
		}
		if requests != nil {
			res["requests"] = requests
		}
		return nil
	}); err != nil {
		return textErrorResult("Error:\n" + err.Error()), nil, nil
	}

	// Update (replace) resource like python rc.replace(...)
	var updated *unstructured.Unstructured
	if namespaced {
		u, err := ri.Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		updated = u
	} else {
		u, err := ri.Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		updated = u
	}

	b, _ := json.MarshalIndent(updated.Object, "", "  ")
	return textOKResult(string(b)), nil, nil
}

// K8sSetImage ports k8s_set_image(resource_type, resource_name, container, image, namespace)
func K8sSetImage(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	resourceType, _ := args["resource_type"].(string)
	resourceName, _ := args["resource_name"].(string)
	containerName, _ := args["container"].(string)
	image, _ := args["image"].(string)
	namespace, _ := args["namespace"].(string)

	if strings.TrimSpace(resourceType) == "" {
		return textErrorResult("resource_type is required"), nil, nil
	}
	if strings.TrimSpace(resourceName) == "" {
		return textErrorResult("resource_name is required"), nil, nil
	}
	if strings.TrimSpace(containerName) == "" {
		return textErrorResult("container is required"), nil, nil
	}
	if strings.TrimSpace(image) == "" {
		return textErrorResult("image is required"), nil, nil
	}
	if namespace == "" {
		namespace = "default"
	}

	disc, err := getDiscovery()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}
	dyn, err := getDynamic()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	gvr, namespaced, found := findGVR(disc, resourceType)
	if !found {
		gvr, namespaced, found = findGVR(disc, resourceType+"s")
	}
	if !found {
		return textErrorResult(fmt.Sprintf("Error: resource '%s' not found in cluster", resourceType)), nil, nil
	}

	ri := dyn.Resource(gvr)

	var obj *unstructured.Unstructured
	if namespaced {
		o, err := ri.Namespace(namespace).Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		obj = o
	} else {
		o, err := ri.Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		obj = o
	}

	kind := strings.ToLower(obj.GetKind())
	if kind == "" {
		kind = strings.ToLower(resourceType)
	}

	var containersPath []string
	switch kind {
	case "deployment", "statefulset", "daemonset", "replicaset":
		containersPath = []string{"spec", "template", "spec", "containers"}
	case "pod":
		containersPath = []string{"spec", "containers"}
	default:
		switch strings.ToLower(resourceType) {
		case "deployment", "statefulset", "daemonset", "replicaset":
			containersPath = []string{"spec", "template", "spec", "containers"}
		case "pod":
			containersPath = []string{"spec", "containers"}
		default:
			return textErrorResult(fmt.Sprintf("Error: resource type '%s' does not support setting image", resourceType)), nil, nil
		}
	}

	containerFound := false
	if err := updateContainers(obj.Object, containersPath, func(c map[string]any) error {
		if fmtAny(c["name"]) != containerName {
			return nil
		}
		c["image"] = image
		containerFound = true
		return nil
	}); err != nil {
		return textErrorResult("Error:\n" + err.Error()), nil, nil
	}

	if !containerFound {
		return textErrorResult(fmt.Sprintf("Error: container '%s' not found in resource '%s/%s'", containerName, resourceType, resourceName)), nil, nil
	}

	var updated *unstructured.Unstructured
	if namespaced {
		u, err := ri.Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		updated = u
	} else {
		u, err := ri.Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		updated = u
	}

	b, _ := json.MarshalIndent(updated.Object, "", "  ")
	return textOKResult(string(b)), nil, nil
}

// K8sSetEnv ports k8s_set_env(resource_type, resource_name, container, env_dict, namespace)
func K8sSetEnv(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	resourceType, _ := args["resource_type"].(string)
	resourceName, _ := args["resource_name"].(string)
	containerName, _ := args["container"].(string)
	namespace, _ := args["namespace"].(string)

	envDict, _ := args["env_dict"].(map[string]any)

	if strings.TrimSpace(resourceType) == "" {
		return textErrorResult("resource_type is required"), nil, nil
	}
	if strings.TrimSpace(resourceName) == "" {
		return textErrorResult("resource_name is required"), nil, nil
	}
	if strings.TrimSpace(containerName) == "" {
		return textErrorResult("container is required"), nil, nil
	}
	if envDict == nil {
		return textErrorResult("env_dict is required (object/map)"), nil, nil
	}
	if namespace == "" {
		namespace = "default"
	}

	disc, err := getDiscovery()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}
	dyn, err := getDynamic()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	gvr, namespaced, found := findGVR(disc, resourceType)
	if !found {
		gvr, namespaced, found = findGVR(disc, resourceType+"s")
	}
	if !found {
		return textErrorResult(fmt.Sprintf("Error: resource '%s' not found in cluster", resourceType)), nil, nil
	}

	ri := dyn.Resource(gvr)

	var obj *unstructured.Unstructured
	if namespaced {
		o, err := ri.Namespace(namespace).Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		obj = o
	} else {
		o, err := ri.Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		obj = o
	}

	kind := strings.ToLower(obj.GetKind())
	if kind == "" {
		kind = strings.ToLower(resourceType)
	}

	var containersPath []string
	switch kind {
	case "deployment", "statefulset", "daemonset", "replicaset":
		containersPath = []string{"spec", "template", "spec", "containers"}
	case "pod":
		containersPath = []string{"spec", "containers"}
	default:
		switch strings.ToLower(resourceType) {
		case "deployment", "statefulset", "daemonset", "replicaset":
			containersPath = []string{"spec", "template", "spec", "containers"}
		case "pod":
			containersPath = []string{"spec", "containers"}
		default:
			return textErrorResult(fmt.Sprintf("Error: resource type '%s' does not support setting environment variables", resourceType)), nil, nil
		}
	}

	containerFound := false
	if err := updateContainers(obj.Object, containersPath, func(c map[string]any) error {
		if fmtAny(c["name"]) != containerName {
			return nil
		}

		// Ensure env exists as []any
		envAny, ok := c["env"].([]any)
		if !ok || envAny == nil {
			envAny = []any{}
		}

		// Index existing by name
		index := map[string]int{}
		for i := range envAny {
			m, _ := envAny[i].(map[string]any)
			if m == nil {
				continue
			}
			n := fmtAny(m["name"])
			if n != "" {
				index[n] = i
			}
		}

		for k, v := range envDict {
			val := fmtAny(v)
			if i, exists := index[k]; exists {
				m, _ := envAny[i].(map[string]any)
				if m == nil {
					m = map[string]any{}
				}
				m["name"] = k
				m["value"] = val
				envAny[i] = m
			} else {
				envAny = append(envAny, map[string]any{"name": k, "value": val})
			}
		}

		c["env"] = envAny
		containerFound = true
		return nil
	}); err != nil {
		return textErrorResult("Error:\n" + err.Error()), nil, nil
	}

	if !containerFound {
		return textErrorResult(fmt.Sprintf("Error: container '%s' not found in resource '%s/%s'", containerName, resourceType, resourceName)), nil, nil
	}

	var updated *unstructured.Unstructured
	if namespaced {
		u, err := ri.Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		updated = u
	} else {
		u, err := ri.Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		updated = u
	}

	b, _ := json.MarshalIndent(updated.Object, "", "  ")
	return textOKResult(string(b)), nil, nil
}

// ---- helpers ----

func updateContainers(root map[string]any, containersPath []string, fn func(container map[string]any) error) error {
	containersAny, found, err := unstructured.NestedSlice(root, containersPath...)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("containers not found at path %v", containersPath)
	}

	for i := range containersAny {
		cm, ok := containersAny[i].(map[string]any)
		if !ok || cm == nil {
			continue
		}
		if err := fn(cm); err != nil {
			return err
		}
		containersAny[i] = cm
	}

	if err := unstructured.SetNestedSlice(root, containersAny, containersPath...); err != nil {
		return err
	}
	return nil
}

func stringSliceFromArgs(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}

	// handle []string
	if ss, ok := v.([]string); ok {
		return ss
	}

	// handle []any from JSON
	if a, ok := v.([]any); ok {
		out := make([]string, 0, len(a))
		for _, x := range a {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}

	// handle comma-separated string
	if s, ok := v.(string); ok && s != "" {
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}

	return nil
}

func stringInSlice(s string, list []string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
