package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
)

// K8sDescribe mirrors describe.py k8s_describe(resource_type, name, namespace, selector, all_namespaces)
func K8sDescribe(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	resourceType, _ := args["resource_type"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	selector, _ := args["selector"].(string)
	allNamespaces := boolFromArgs(args, "all_namespaces", false)

	if strings.TrimSpace(resourceType) == "" {
		return textErrorResult("resource_type is required"), nil, nil
	}

	// Default namespace like Python (only if not all namespaces)
	if !allNamespaces && namespace == "" {
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
	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	gvr, namespaced, found := findGVR(disc, resourceType)
	if !found {
		return textErrorResult(fmt.Sprintf("Error: resource type '%s' not found", resourceType)), nil, nil
	}

	ri := dyn.Resource(gvr)

	// Describe single object by name
	if name != "" {
		var obj *unstructured.Unstructured

		if namespaced {
			if allNamespaces {
				list, err := ri.Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
					LabelSelector: selector,
				})
				if err != nil {
					return textErrorResult(formatK8sErr(err)), nil, nil
				}

				for i := range list.Items {
					if list.Items[i].GetName() == name {
						o := list.Items[i]
						obj = &o
						break
					}
				}
				if obj == nil {
					return textErrorResult(fmt.Sprintf("Error: %s '%s' not found in any namespace", resourceType, name)), nil, nil
				}
			} else {
				o, err := ri.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					return textErrorResult(formatK8sErr(err)), nil, nil
				}
				obj = o
			}
		} else {
			o, err := ri.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return textErrorResult(formatK8sErr(err)), nil, nil
			}
			obj = o
		}

		desc := formatResourceDescription(obj)

		evs := fetchEventsForObject(ctx, cs, obj)
		if len(evs) > 0 {
			desc += "\nEvents:\n"
			for _, e := range evs {
				ts := formatEventTime(e)
				desc += fmt.Sprintf("  %s: %s %s: %s\n", ts, e.Type, e.Reason, e.Message)
			}
		}

		return textOKResult(desc), nil, nil
	}

	// Describe list (matching selector)
	var list *unstructured.UnstructuredList
	if namespaced {
		ns := namespace
		if allNamespaces {
			ns = metav1.NamespaceAll
		}
		l, err := ri.Namespace(ns).List(ctx, metav1.ListOptions{
			LabelSelector: selector,
		})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		list = l
	} else {
		l, err := ri.List(ctx, metav1.ListOptions{
			LabelSelector: selector,
		})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		list = l
	}

	if len(list.Items) == 0 {
		return textOKResult(fmt.Sprintf("No %s found", resourceType)), nil, nil
	}

	var parts []string
	for i := range list.Items {
		obj := &list.Items[i]
		desc := formatResourceDescription(obj)

		evs := fetchEventsForObject(ctx, cs, obj)
		if len(evs) > 0 {
			desc += "\nEvents:\n"
			for _, e := range evs {
				ts := formatEventTime(e)
				desc += fmt.Sprintf("  %s: %s %s: %s\n", ts, e.Type, e.Reason, e.Message)
			}
		}

		parts = append(parts, desc)
	}

	return textOKResult(strings.Join(parts, "\n\n")), nil, nil
}

// ---- Events (typed clientset) ----

func fetchEventsForObject(ctx context.Context, cs *kubernetes.Clientset, obj *unstructured.Unstructured) []eventLike {
	name := obj.GetName()
	ns := obj.GetNamespace()

	fieldSelector := "involvedObject.name=" + name
	if ns != "" {
		fieldSelector += ",involvedObject.namespace=" + ns
	}

	// Events are namespaced; for cluster-scoped objects, we have to search all namespaces.
	evNS := ns
	if evNS == "" {
		evNS = metav1.NamespaceAll
	}

	events, err := cs.CoreV1().Events(evNS).List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return nil
	}

	out := make([]eventLike, 0, len(events.Items))
	for _, e := range events.Items {
		out = append(out, eventLike{
			Type:         e.Type,
			Reason:       e.Reason,
			Message:      e.Message,
			First:        e.FirstTimestamp,
			Last:         e.LastTimestamp,
			EventTime:    e.EventTime,
			CreationTime: e.CreationTimestamp,
		})
	}
	return out
}

type eventLike struct {
	Type         string
	Reason       string
	Message      string
	First        metav1.Time
	Last         metav1.Time
	EventTime    metav1.MicroTime
	CreationTime metav1.Time
}

func formatEventTime(e eventLike) string {
	if !e.Last.Time.IsZero() {
		return e.Last.Time.UTC().Format(time.RFC3339)
	}
	if !e.EventTime.Time.IsZero() {
		return e.EventTime.Time.UTC().Format(time.RFC3339)
	}
	if !e.First.Time.IsZero() {
		return e.First.Time.UTC().Format(time.RFC3339)
	}
	if !e.CreationTime.Time.IsZero() {
		return e.CreationTime.Time.UTC().Format(time.RFC3339)
	}
	return ""
}

// ---- formatting (simple + useful; you can enhance later) ----

func formatResourceDescription(obj *unstructured.Unstructured) string {
	kind := obj.GetKind()
	if kind == "" {
		kind = "Resource"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s: %s\n", kind, obj.GetName()))

	if ns := obj.GetNamespace(); ns != "" {
		b.WriteString(fmt.Sprintf("Namespace: %s\n", ns))
	}

	if labels := obj.GetLabels(); len(labels) > 0 {
		b.WriteString("Labels:\n")
		for k, v := range labels {
			b.WriteString(fmt.Sprintf("  %s=%s\n", k, v))
		}
	}

	if ann := obj.GetAnnotations(); len(ann) > 0 {
		b.WriteString("Annotations:\n")
		for k, v := range ann {
			b.WriteString(fmt.Sprintf("  %s=%s\n", k, v))
		}
	}

	ct := obj.GetCreationTimestamp().Time
	if !ct.IsZero() {
		b.WriteString(fmt.Sprintf("Creation Timestamp: %s\n",
			ct.UTC().Format(time.RFC3339)))
	}

	// Keep it best-effort and safe. For deeper per-kind output, we can extend later
	// once we see exactly what describe.py prints in your repo.
	return b.String()
}
