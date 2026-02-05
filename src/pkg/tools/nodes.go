package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// K8sCordon sets spec.unschedulable=true on the node.
func K8sCordon(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	nodeName, _ := args["node_name"].(string)
	if nodeName == "" {
		return textErrorResult("node_name is required"), nil, nil
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	patch := map[string]any{
		"spec": map[string]any{
			"unschedulable": true,
		},
	}
	data, _ := json.Marshal(patch)

	if _, err := cs.CoreV1().Nodes().Patch(ctx, nodeName, types.StrategicMergePatchType, data, metav1.PatchOptions{}); err != nil {
		return textErrorResult(fmt.Sprintf("Error cordoning node %s: %v", nodeName, err)), nil, nil
	}

	return textOKResult(fmt.Sprintf("Node %s cordoned successfully", nodeName)), nil, nil
}

// K8sUncordon sets spec.unschedulable=false on the node.
func K8sUncordon(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	nodeName, _ := args["node_name"].(string)
	if nodeName == "" {
		return textErrorResult("node_name is required"), nil, nil
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	patch := map[string]any{
		"spec": map[string]any{
			"unschedulable": false,
		},
	}
	data, _ := json.Marshal(patch)

	if _, err := cs.CoreV1().Nodes().Patch(ctx, nodeName, types.StrategicMergePatchType, data, metav1.PatchOptions{}); err != nil {
		return textErrorResult(fmt.Sprintf("Error uncordoning node %s: %v", nodeName, err)), nil, nil
	}

	return textOKResult(fmt.Sprintf("Node %s uncordoned successfully", nodeName)), nil, nil
}

// K8sDrain is a drain implementation closer to `kubectl drain`:
// - cordons the node (unschedulable=true)
// - lists pods on the node
// - skips mirror/static pods
// - optionally ignores DaemonSets
// - uses the Eviction API (policy/v1) => PDB-aware
// - retries on 429 TooManyRequests until timeout
// - optional force delete fallback when eviction fails
//
// Args (all optional except node_name):
// - node_name (string) required
// - ignore_daemonsets (bool) default false
// - delete_local_data (bool) default false
// - force (bool) default false
// - grace_period (int) default unset (pod default). If >=0, sets GracePeriodSeconds.
// - timeout_seconds (int) default 600
// - retry_backoff_ms (int) default 1000
// - max_backoff_ms (int) default 10000
func K8sDrain(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	nodeName, _ := args["node_name"].(string)
	if nodeName == "" {
		return textErrorResult("node_name is required"), nil, nil
	}

	ignoreDaemonsets := boolFromArgs(args, "ignore_daemonsets", false)
	deleteLocalData := boolFromArgs(args, "delete_local_data", false)
	force := boolFromArgs(args, "force", false)

	timeoutSeconds := intFromArgsDefault(args, "timeout_seconds", 600)
	retryBackoffMS := intFromArgsDefault(args, "retry_backoff_ms", 1000)
	maxBackoffMS := intFromArgsDefault(args, "max_backoff_ms", 10000)

	var gracePtr *int64
	if gp, ok := intFromArgs(args, "grace_period"); ok {
		// negative values mean "use pod default"
		if gp >= 0 {
			gp64 := int64(gp)
			gracePtr = &gp64
		}
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	// 1) Cordon the node first
	if res, _, _ := K8sCordon(ctx, nil, map[string]any{"node_name": nodeName}); res.IsError {
		return res, nil, nil
	}

	// 2) List pods on the node across all namespaces
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		return textErrorResult(fmt.Sprintf("Error listing pods on node %s: %v", nodeName, err)), nil, nil
	}

	type podResult struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		Action    string `json:"action"`
		Error     string `json:"error,omitempty"`
	}

	// Drain deadline
	drainCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	var results []podResult

	for _, pod := range pods.Items {
		// Skip completed pods
		if isCompletedPod(&pod) {
			continue
		}

		// Skip mirror/static pods (kubelet static pods)
		if isMirrorPod(&pod) {
			results = append(results, podResult{
				Namespace: pod.Namespace,
				Name:      pod.Name,
				Action:    "skipped (mirror/static pod)",
			})
			continue
		}

		// Skip DaemonSet-managed pods if configured
		if ignoreDaemonsets && isOwnedBy(&pod, "DaemonSet") {
			results = append(results, podResult{
				Namespace: pod.Namespace,
				Name:      pod.Name,
				Action:    "skipped (daemonset)",
			})
			continue
		}

		// Local data guard: emptyDir/hostPath volumes
		if !deleteLocalData && hasLocalData(&pod) && !force {
			results = append(results, podResult{
				Namespace: pod.Namespace,
				Name:      pod.Name,
				Action:    "skipped (local data; set delete_local_data=true or force=true)",
			})
			continue
		}

		// 3) Evict (PDB-aware). Retry on 429 until timeout.
		if err := evictWithRetry(drainCtx, cs, &pod, gracePtr,
			time.Duration(retryBackoffMS)*time.Millisecond,
			time.Duration(maxBackoffMS)*time.Millisecond,
		); err != nil {
			// Optional force fallback: delete directly if eviction fails and force=true
			if force {
				delOpts := metav1.DeleteOptions{}
				if gracePtr != nil {
					delOpts.GracePeriodSeconds = gracePtr
				}
				if derr := cs.CoreV1().Pods(pod.Namespace).Delete(drainCtx, pod.Name, delOpts); derr != nil {
					results = append(results, podResult{
						Namespace: pod.Namespace,
						Name:      pod.Name,
						Action:    "evict_failed_delete_failed",
						Error:     fmt.Sprintf("evict: %v; delete: %v", err, derr),
					})
					continue
				}
				results = append(results, podResult{
					Namespace: pod.Namespace,
					Name:      pod.Name,
					Action:    "force_deleted",
				})
				continue
			}

			results = append(results, podResult{
				Namespace: pod.Namespace,
				Name:      pod.Name,
				Action:    "evict_failed",
				Error:     err.Error(),
			})
			continue
		}

		results = append(results, podResult{
			Namespace: pod.Namespace,
			Name:      pod.Name,
			Action:    "evicted",
		})
	}

	summary := map[string]any{
		"node":              nodeName,
		"status":            "drain_attempted",
		"ignore_daemonsets": ignoreDaemonsets,
		"delete_local_data": deleteLocalData,
		"force":             force,
		"grace_period":      gracePtr,
		"timeout_seconds":   timeoutSeconds,
		"retry_backoff_ms":  retryBackoffMS,
		"max_backoff_ms":    maxBackoffMS,
		"results":           results,
	}

	data, _ := json.MarshalIndent(summary, "", "  ")
	return textOKResult(string(data)), nil, nil
}

func evictWithRetry(
	ctx context.Context,
	cs *kubernetes.Clientset,
	pod *v1.Pod,
	gracePtr *int64,
	initialBackoff time.Duration,
	maxBackoff time.Duration,
) error {
	backoff := initialBackoff
	if backoff <= 0 {
		backoff = time.Second
	}
	if maxBackoff <= 0 {
		maxBackoff = 10 * time.Second
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		ev := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		}
		if gracePtr != nil {
			ev.DeleteOptions = &metav1.DeleteOptions{GracePeriodSeconds: gracePtr}
		}

		err := cs.PolicyV1().Evictions(pod.Namespace).Evict(ctx, ev)
		if err == nil {
			// Eviction accepted; wait for deletion
			return waitPodDeleted(ctx, cs, pod.Namespace, pod.Name)
		}

		if apierrors.IsNotFound(err) {
			return nil
		}

		// PDB throttle => 429
		if apierrors.IsTooManyRequests(err) {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// Retry some transient states
		if apierrors.IsConflict(err) || apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		return err
	}
}

func waitPodDeleted(ctx context.Context, cs *kubernetes.Clientset, namespace, name string) error {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()

	for {
		_, err := cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ---- helpers for drain ----

func boolFromArgs(args map[string]any, key string, def bool) bool {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		if t == "true" || t == "1" {
			return true
		}
		if t == "false" || t == "0" {
			return false
		}
	case float64:
		return t != 0
	}
	return def
}

func intFromArgs(args map[string]any, key string) (int, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		var i int
		_, err := fmt.Sscanf(t, "%d", &i)
		if err == nil {
			return i, true
		}
	}
	return 0, false
}

func intFromArgsDefault(args map[string]any, key string, def int) int {
	if v, ok := intFromArgs(args, key); ok {
		return v
	}
	return def
}

func isCompletedPod(pod *v1.Pod) bool {
	return pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed
}

func isOwnedBy(pod *v1.Pod, kind string) bool {
	for _, or := range pod.OwnerReferences {
		if or.Kind == kind {
			return true
		}
	}
	return false
}

func hasLocalData(pod *v1.Pod) bool {
	for _, v := range pod.Spec.Volumes {
		if v.EmptyDir != nil || v.HostPath != nil {
			return true
		}
	}
	return false
}

func isMirrorPod(pod *v1.Pod) bool {
	_, ok := pod.Annotations["kubernetes.io/config.mirror"]
	return ok
}
