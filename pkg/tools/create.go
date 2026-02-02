package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
)

// One entry per YAML document/object (mirrors create.py behavior).
type createResult struct {
	Status  string         `json:"status"`
	Message string         `json:"message,omitempty"`
	Object  map[string]any `json:"object,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	GVR     string         `json:"gvr,omitempty"`
}

// K8sCreate: MCP tool handler.
// Python: k8s_create(yaml_content, namespace=None)
func K8sCreate(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	yamlContent := getStringArg(args, "yaml_content", "yaml")
	namespace := getStringArg(args, "namespace")

	out, err := k8sCreateOrApply(ctx, yamlContent, namespace, false)
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}
	return textOKResult(out), nil, nil
}

// K8sApply: MCP tool handler (Server-Side Apply).
// Python: k8s_apply(yaml_content, namespace=None)
func K8sApply(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	yamlContent := getStringArg(args, "yaml_content", "yaml")
	namespace := getStringArg(args, "namespace")

	out, err := k8sCreateOrApply(ctx, yamlContent, namespace, true)
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}
	return textOKResult(out), nil, nil
}

func k8sCreateOrApply(ctx context.Context, yamlContent string, namespace string, apply bool) (string, error) {
	if strings.TrimSpace(yamlContent) == "" {
		// Keep consistent with your other tools: return an error-ish message but not Go error.
		// (If you prefer IsError=true, we can flip this.)
		return `{"error":"No valid YAML/JSON content provided"}`, nil
	}

	dyn, err := GetDynamicClient()
	if err != nil {
		return "", err
	}
	mapper, err := GetRESTMapper()
	if err != nil {
		return "", err
	}

	dec := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(yamlContent), 4096)

	results := make([]createResult, 0, 4)

	for {
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			results = append(results, createResult{
				Status:  "error",
				Message: fmt.Sprintf("decode error: %v", err),
			})
			break
		}
		if len(raw) == 0 {
			continue
		}

		u := &unstructured.Unstructured{Object: raw}

		apiVersion := u.GetAPIVersion()
		kind := u.GetKind()
		if apiVersion == "" || kind == "" {
			results = append(results, createResult{
				Status:  "error",
				Message: "object missing apiVersion/kind",
				Object:  raw,
			})
			continue
		}

		gvk := schema.FromAPIVersionAndKind(apiVersion, kind)
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			results = append(results, createResult{
				Status:  "error",
				Message: fmt.Sprintf("cannot map GVK %s: %v", gvk.String(), err),
				Object:  raw,
			})
			continue
		}

		// Namespace override (only for namespaced resources)
		var ns string
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			if namespace != "" {
				u.SetNamespace(namespace)
			}
			ns = u.GetNamespace()
			if ns == "" {
				ns = "default"
				u.SetNamespace(ns)
			}
		} else {
			u.SetNamespace("")
		}

		gvr := mapping.Resource

		// Important: dynamic.Interface.Resource(...) returns NamespaceableResourceInterface,
		// but Create/Patch are on ResourceInterface. Keep it typed correctly.
		var resIf dynamic.ResourceInterface
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			resIf = dyn.Resource(gvr).Namespace(ns)
		} else {
			resIf = dyn.Resource(gvr)
		}

		if apply {
			name := u.GetName()
			if name == "" {
				results = append(results, createResult{
					Status:  "error",
					Message: "apply requires metadata.name",
					Object:  raw,
					GVR:     gvr.String(),
				})
				continue
			}

			patchBytes, err := json.Marshal(u.Object)
			if err != nil {
				results = append(results, createResult{
					Status:  "error",
					Message: fmt.Sprintf("marshal error: %v", err),
					Object:  raw,
					GVR:     gvr.String(),
				})
				continue
			}

			force := true
			out, err := resIf.Patch(ctx, name, types.ApplyPatchType, patchBytes, metav1.PatchOptions{
				FieldManager: "mcp-k8s",
				Force:        &force,
			})
			if err != nil {
				results = append(results, createResult{
					Status:  "error",
					Message: err.Error(),
					Object:  raw,
					GVR:     gvr.String(),
				})
				continue
			}

			results = append(results, createResult{
				Status: "applied",
				Result: out.Object,
				GVR:    gvr.String(),
			})
			continue
		}

		out, err := resIf.Create(ctx, u, metav1.CreateOptions{})
		if err != nil {
			results = append(results, createResult{
				Status:  "error",
				Message: err.Error(),
				Object:  raw,
				GVR:     gvr.String(),
			})
			continue
		}

		results = append(results, createResult{
			Status: "created",
			Result: out.Object,
			GVR:    gvr.String(),
		})
	}

	pretty, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", err
	}
	return string(pretty), nil
}
