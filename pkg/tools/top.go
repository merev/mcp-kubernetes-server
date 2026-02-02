package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type topNodeRow struct {
	Name   string `json:"name"`
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

type topPodRow struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	CPU       string `json:"cpu"`
	Memory    string `json:"memory"`
}

// K8sTopNodes: MCP tool handler.
// Args (compatible with your python): sort_by
func K8sTopNodes(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	if err := SetupClient(ctx); err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	sortBy := getStringArg(args, "sort_by", "sortBy")
	out, err := k8sTopNodes(ctx, sortBy)
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}
	return textOKResult(out), nil, nil
}

// K8sTopPods: MCP tool handler.
// Args (compatible with your python): namespace, all_namespaces, sort_by, selector
func K8sTopPods(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	if err := SetupClient(ctx); err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	namespace := getStringArg(args, "namespace")
	allNamespaces := getBoolArg(args, "all_namespaces", "allNamespaces")
	sortBy := getStringArg(args, "sort_by", "sortBy")
	selector := getStringArg(args, "selector")

	out, err := k8sTopPods(ctx, namespace, allNamespaces, sortBy, selector)
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}
	return textOKResult(out), nil, nil
}

func k8sTopNodes(ctx context.Context, sortBy string) (string, error) {
	cs, err := getClient()
	if err != nil {
		return "", err
	}
	dyn, err := getDynamic()
	if err != nil {
		return "", err
	}

	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}

	gvr := schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"}
	metricsList, err := dyn.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list node metrics (metrics.k8s.io): %w", err)
	}

	metricsByName := map[string]*unstructured.Unstructured{}
	for i := range metricsList.Items {
		m := &metricsList.Items[i]
		metricsByName[m.GetName()] = m
	}

	out := make([]topNodeRow, 0, len(nodes.Items))

	for _, node := range nodes.Items {
		m := metricsByName[node.Name]
		if m == nil {
			continue
		}

		usageCPU, usageMem, ok := extractNodeUsage(m)
		if !ok {
			continue
		}

		capCPUQ, ok := node.Status.Capacity["cpu"]
		if !ok {
			continue
		}
		capMemQ, ok := node.Status.Capacity["memory"]
		if !ok {
			continue
		}

		capMil := capCPUQ.MilliValue()
		usageMil := usageCPU.MilliValue()
		cpuPct := 0.0
		if capMil > 0 {
			cpuPct = (float64(usageMil) / float64(capMil)) * 100
		}

		capBytes := capMemQ.Value()
		usageBytes := usageMem.Value()
		memPct := 0.0
		if capBytes > 0 {
			memPct = (float64(usageBytes) / float64(capBytes)) * 100
		}

		out = append(out, topNodeRow{
			Name:   node.Name,
			CPU:    fmt.Sprintf("%dm (%.0f%%)", usageMil, cpuPct),
			Memory: fmt.Sprintf("%s (%.0f%%)", formatBytesHuman(usageBytes), memPct),
		})
	}

	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	switch sortBy {
	case "cpu":
		sort.Slice(out, func(i, j int) bool {
			return extractPct(out[i].CPU) > extractPct(out[j].CPU)
		})
	case "memory":
		sort.Slice(out, func(i, j int) bool {
			return extractPct(out[i].Memory) > extractPct(out[j].Memory)
		})
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func k8sTopPods(ctx context.Context, namespace string, allNamespaces bool, sortBy string, selector string) (string, error) {
	cs, err := getClient()
	if err != nil {
		return "", err
	}
	dyn, err := getDynamic()
	if err != nil {
		return "", err
	}

	if !allNamespaces && strings.TrimSpace(namespace) == "" {
		namespace = "default"
	}

	// pods list (typed, for selection + namespace/name)
	var pods []struct {
		Name      string
		Namespace string
	}

	if allNamespaces {
		podList, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return "", fmt.Errorf("list pods (all namespaces): %w", err)
		}
		pods = make([]struct {
			Name      string
			Namespace string
		}, 0, len(podList.Items))
		for _, p := range podList.Items {
			pods = append(pods, struct {
				Name      string
				Namespace string
			}{Name: p.Name, Namespace: p.Namespace})
		}
	} else {
		podList, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return "", fmt.Errorf("list pods in namespace %q: %w", namespace, err)
		}
		pods = make([]struct {
			Name      string
			Namespace string
		}, 0, len(podList.Items))
		for _, p := range podList.Items {
			pods = append(pods, struct {
				Name      string
				Namespace string
			}{Name: p.Name, Namespace: p.Namespace})
		}
	}

	// metrics list (dynamic)
	gvr := schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}

	var metricsList *unstructured.UnstructuredList
	if allNamespaces {
		ml, err := dyn.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", fmt.Errorf("list pod metrics (all namespaces): %w", err)
		}
		metricsList = ml
	} else {
		ml, err := dyn.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", fmt.Errorf("list pod metrics in namespace %q: %w", namespace, err)
		}
		metricsList = ml
	}

	metricsByNSName := map[string]*unstructured.Unstructured{}
	for i := range metricsList.Items {
		m := &metricsList.Items[i]
		key := m.GetNamespace() + "/" + m.GetName()
		metricsByNSName[key] = m
	}

	out := make([]topPodRow, 0, len(pods))

	for _, p := range pods {
		key := p.Namespace + "/" + p.Name
		m := metricsByNSName[key]
		if m == nil {
			continue
		}

		totalMil, totalBytes, ok := sumPodUsage(m)
		if !ok {
			continue
		}

		out = append(out, topPodRow{
			Name:      p.Name,
			Namespace: p.Namespace,
			CPU:       fmt.Sprintf("%dm", totalMil),
			Memory:    formatBytesHuman(totalBytes),
		})
	}

	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	switch sortBy {
	case "cpu":
		sort.Slice(out, func(i, j int) bool {
			return parseMilli(out[i].CPU) > parseMilli(out[j].CPU)
		})
	case "memory":
		sort.Slice(out, func(i, j int) bool {
			return parseMemBytes(out[i].Memory) > parseMemBytes(out[j].Memory)
		})
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func extractNodeUsage(m *unstructured.Unstructured) (cpu resource.Quantity, mem resource.Quantity, ok bool) {
	usage, found, err := unstructured.NestedStringMap(m.Object, "usage")
	if err != nil || !found {
		return cpu, mem, false
	}
	cpuStr, ok1 := usage["cpu"]
	memStr, ok2 := usage["memory"]
	if !ok1 || !ok2 {
		return cpu, mem, false
	}
	c, err := resource.ParseQuantity(cpuStr)
	if err != nil {
		return cpu, mem, false
	}
	me, err := resource.ParseQuantity(memStr)
	if err != nil {
		return cpu, mem, false
	}
	return c, me, true
}

func sumPodUsage(m *unstructured.Unstructured) (totalMil int64, totalBytes int64, ok bool) {
	containers, found, err := unstructured.NestedSlice(m.Object, "containers")
	if err != nil || !found {
		return 0, 0, false
	}

	var mil int64
	var bytes int64

	for _, c := range containers {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		usage, ok := cm["usage"].(map[string]any)
		if !ok {
			continue
		}
		cpuStr, _ := usage["cpu"].(string)
		memStr, _ := usage["memory"].(string)
		if cpuStr == "" || memStr == "" {
			continue
		}

		cpuQ, err := resource.ParseQuantity(cpuStr)
		if err == nil {
			mil += cpuQ.MilliValue()
		}
		memQ, err := resource.ParseQuantity(memStr)
		if err == nil {
			bytes += memQ.Value()
		}
	}

	return mil, bytes, true
}

func formatBytesHuman(b int64) string {
	const (
		mi = 1024 * 1024
		gi = 1024 * 1024 * 1024
	)
	if b >= gi {
		return fmt.Sprintf("%.0fGi", float64(b)/float64(gi))
	}
	return fmt.Sprintf("%.0fMi", float64(b)/float64(mi))
}

func extractPct(s string) float64 {
	// expects "... (NN%)"
	i := strings.Index(s, "(")
	j := strings.Index(s, "%")
	if i < 0 || j < 0 || j <= i {
		return 0
	}
	part := strings.TrimSpace(s[i+1 : j])
	v, _ := strconv.ParseFloat(part, 64)
	return v
}

func parseMilli(cpu string) float64 {
	// "123m"
	cpu = strings.TrimSpace(strings.TrimSuffix(cpu, "m"))
	v, _ := strconv.ParseFloat(cpu, 64)
	return v
}

func parseMemBytes(mem string) float64 {
	mem = strings.TrimSpace(mem)
	if strings.HasSuffix(mem, "Gi") {
		v, _ := strconv.ParseFloat(strings.TrimSuffix(mem, "Gi"), 64)
		return v * 1024 * 1024 * 1024
	}
	if strings.HasSuffix(mem, "Mi") {
		v, _ := strconv.ParseFloat(strings.TrimSuffix(mem, "Mi"), 64)
		return v * 1024 * 1024
	}
	return 0
}

// keep the compiler honest about imports
var _ dynamic.Interface
