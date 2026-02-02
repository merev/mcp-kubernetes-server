package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// K8sEvents ports events.py k8s_events(...)
func K8sEvents(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	namespace, _ := args["namespace"].(string)
	allNamespaces := boolFromArgs(args, "all_namespaces", false)
	fieldSelector, _ := args["field_selector"].(string)
	resourceType, _ := args["resource_type"].(string)
	resourceName, _ := args["resource_name"].(string)
	sortBy, _ := args["sort_by"].(string)
	watchMode := boolFromArgs(args, "watch", false)

	// Default namespace like python
	if !allNamespaces && namespace == "" {
		namespace = "default"
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	// Build field selector (python appends involvedObject filters)
	apiFieldSelector := strings.TrimSpace(fieldSelector)
	if resourceType != "" && resourceName != "" {
		kind := kindFromResourceType(resourceType)
		resourceSel := fmt.Sprintf("involvedObject.kind=%s,involvedObject.name=%s", kind, resourceName)
		if apiFieldSelector != "" {
			apiFieldSelector = apiFieldSelector + "," + resourceSel
		} else {
			apiFieldSelector = resourceSel
		}
	}

	if watchMode {
		return k8sEventsWatch(ctx, cs, namespace, allNamespaces, apiFieldSelector)
	}

	return k8sEventsList(ctx, cs, namespace, allNamespaces, apiFieldSelector, sortBy)
}

func k8sEventsList(ctx context.Context, cs *kubernetes.Clientset, namespace string, allNamespaces bool, fieldSelector string, sortBy string) (*mcp.CallToolResult, any, error) {
	evNS := namespace
	if allNamespaces {
		evNS = metav1.NamespaceAll
	}

	evs, err := cs.CoreV1().Events(evNS).List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return textErrorResult("Error:\n" + err.Error()), nil, nil
	}

	items := make([]map[string]any, 0, len(evs.Items))
	for _, e := range evs.Items {
		m := map[string]any{
			"type":    e.Type,
			"reason":  e.Reason,
			"object":  fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name),
			"message": e.Message,
			"count":   e.Count,
			"source":  e.Source.Component,
		}

		if allNamespaces {
			m["namespace"] = e.Namespace
		}

		m["first_timestamp"] = formatMetaTime(e.FirstTimestamp)
		m["last_timestamp"] = formatMetaTime(e.LastTimestamp)

		items = append(items, m)
	}

	applyEventSort(items, sortBy)

	b, _ := json.MarshalIndent(items, "", "  ")
	return textOKResult(string(b)), nil, nil
}

func k8sEventsWatch(ctx context.Context, cs *kubernetes.Clientset, namespace string, allNamespaces bool, fieldSelector string) (*mcp.CallToolResult, any, error) {
	// Match python: watch up to ~10 seconds, 1MB cap
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	evNS := namespace
	if allNamespaces {
		evNS = metav1.NamespaceAll
	}

	// Initial list (also gets resourceVersion)
	initial, err := cs.CoreV1().Events(evNS).List(wctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return textErrorResult("Error:\n" + err.Error()), nil, nil
	}

	const maxBytes = 1024 * 1024
	var sb strings.Builder

	// Print initial events
	for _, e := range initial.Items {
		line := formatEventLine(&e, "")
		if sb.Len()+len(line) > maxBytes {
			sb.WriteString("\n... event output truncated ...\n")
			return textOKResult(sb.String()), nil, nil
		}
		sb.WriteString(line)
	}

	// Watch from RV
	w, err := cs.CoreV1().Events(evNS).Watch(wctx, metav1.ListOptions{
		FieldSelector:   fieldSelector,
		ResourceVersion: initial.ResourceVersion,
	})
	if err != nil {
		sb.WriteString("\n... watch ended: " + err.Error() + " ...\n")
		return textOKResult(sb.String()), nil, nil
	}
	defer w.Stop()

	ch := w.ResultChan()

	for {
		select {
		case <-wctx.Done():
			sb.WriteString("\n... watch ended: timeout ...\n")
			return textOKResult(sb.String()), nil, nil

		case ev, ok := <-ch:
			if !ok {
				sb.WriteString("\n... watch ended: channel closed ...\n")
				return textOKResult(sb.String()), nil, nil
			}

			// watch delivers runtime.Object; for core/v1 Events it's *v1.Event
			obj, ok := ev.Object.(*v1.Event)
			if !ok || obj == nil {
				continue
			}

			line := formatEventLine(obj, string(ev.Type))
			if sb.Len()+len(line) > maxBytes {
				sb.WriteString("\n... event output truncated ...\n")
				return textOKResult(sb.String()), nil, nil
			}
			sb.WriteString(line)
		}
	}
}

// ---- Sorting (matches your python sort_by options) ----

func applyEventSort(items []map[string]any, sortBy string) {
	if sortBy == "" {
		return
	}
	switch strings.ToLower(sortBy) {
	case "lasttimestamp":
		sort.Slice(items, func(i, j int) bool {
			return fmt.Sprint(items[i]["last_timestamp"]) > fmt.Sprint(items[j]["last_timestamp"])
		})
	case "firsttimestamp":
		sort.Slice(items, func(i, j int) bool {
			return fmt.Sprint(items[i]["first_timestamp"]) > fmt.Sprint(items[j]["first_timestamp"])
		})
	case "count":
		sort.Slice(items, func(i, j int) bool {
			ai := toInt64(items[i]["count"])
			aj := toInt64(items[j]["count"])
			return ai > aj
		})
	case "type":
		sort.Slice(items, func(i, j int) bool {
			return fmt.Sprint(items[i]["type"]) < fmt.Sprint(items[j]["type"])
		})
	case "reason":
		sort.Slice(items, func(i, j int) bool {
			return fmt.Sprint(items[i]["reason"]) < fmt.Sprint(items[j]["reason"])
		})
	case "object":
		sort.Slice(items, func(i, j int) bool {
			return fmt.Sprint(items[i]["object"]) < fmt.Sprint(items[j]["object"])
		})
	case "source":
		sort.Slice(items, func(i, j int) bool {
			return fmt.Sprint(items[i]["source"]) < fmt.Sprint(items[j]["source"])
		})
	}
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case float64:
		return int64(t)
	default:
		return 0
	}
}

// ---- Formatting helpers ----

func kindFromResourceType(rt string) string {
	rt = strings.TrimSpace(rt)
	if rt == "" {
		return ""
	}

	l := strings.ToLower(rt)
	if strings.HasSuffix(l, "ies") {
		l = strings.TrimSuffix(l, "ies") + "y"
	} else if strings.HasSuffix(l, "s") {
		l = strings.TrimSuffix(l, "s")
	}
	return strings.ToUpper(l[:1]) + l[1:]
}

func formatEventLine(e *v1.Event, watchType string) string {
	ts := eventTimestamp(e)
	line := fmt.Sprintf("%s %s %s %s/%s: %s",
		ts,
		e.Type,
		e.Reason,
		e.InvolvedObject.Kind,
		e.InvolvedObject.Name,
		e.Message,
	)
	if watchType != "" {
		line += fmt.Sprintf(" (%s)", watchType)
	}
	return line + "\n"
}

func eventTimestamp(e *v1.Event) string {
	// Prefer last/first timestamps; fall back to creationTimestamp
	if !e.LastTimestamp.Time.IsZero() {
		return e.LastTimestamp.Time.UTC().Format(time.RFC3339)
	}
	if !e.EventTime.Time.IsZero() {
		return e.EventTime.Time.UTC().Format(time.RFC3339)
	}
	if !e.FirstTimestamp.Time.IsZero() {
		return e.FirstTimestamp.Time.UTC().Format(time.RFC3339)
	}
	if !e.CreationTimestamp.Time.IsZero() {
		return e.CreationTimestamp.Time.UTC().Format(time.RFC3339)
	}
	return ""
}

func formatMetaTime(t metav1.Time) string {
	if t.Time.IsZero() {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}

// Ensure imports remain used if you later remove watch mode streaming with bufio
var _ = bufio.NewReader
var _ = io.EOF
var _ watch.EventType
