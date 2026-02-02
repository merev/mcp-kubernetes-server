package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
)

// K8sRolloutStatus ports k8s_rollout_status(resource_type, name, namespace)
func K8sRolloutStatus(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	resourceType, _ := args["resource_type"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)

	if strings.TrimSpace(resourceType) == "" {
		return textErrorResult("resource_type is required"), nil, nil
	}
	if strings.TrimSpace(name) == "" {
		return textErrorResult("name is required"), nil, nil
	}
	if namespace == "" {
		namespace = "default"
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	switch strings.ToLower(resourceType) {
	case "deployment":
		d, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}

		replicas := int32(0)
		if d.Status.Replicas != 0 {
			replicas = d.Status.Replicas
		}
		ready := int32(0)
		if d.Status.ReadyReplicas != 0 {
			ready = d.Status.ReadyReplicas
		}
		updated := int32(0)
		if d.Status.UpdatedReplicas != 0 {
			updated = d.Status.UpdatedReplicas
		}
		avail := int32(0)
		if d.Status.AvailableReplicas != 0 {
			avail = d.Status.AvailableReplicas
		}

		conds := make([]map[string]any, 0, len(d.Status.Conditions))
		for _, c := range d.Status.Conditions {
			conds = append(conds, map[string]any{
				"type":                 string(c.Type),
				"status":               string(c.Status),
				"reason":               c.Reason,
				"message":              c.Message,
				"last_update_time":     c.LastUpdateTime.Time.UTC().Format(time.RFC3339),
				"last_transition_time": c.LastTransitionTime.Time.UTC().Format(time.RFC3339),
			})
		}

		status := map[string]any{
			"name":               d.Name,
			"namespace":          d.Namespace,
			"replicas":           replicas,
			"ready_replicas":     ready,
			"updated_replicas":   updated,
			"available_replicas": avail,
			"conditions":         conds,
		}

		if ready == replicas && updated == replicas && avail == replicas {
			status["status"] = "complete"
			status["message"] = fmt.Sprintf(`deployment "%s" successfully rolled out`, name)
		} else {
			status["status"] = "in progress"
			msg := fmt.Sprintf(`Waiting for deployment "%s" rollout to finish: %d out of %d new replicas have been updated...`, name, updated, replicas)
			if avail < updated {
				msg += fmt.Sprintf("\n%d available replicas are ready...", avail)
			}
			status["message"] = msg
		}

		b, _ := json.MarshalIndent(status, "", "  ")
		return textOKResult(string(b)), nil, nil

	case "daemonset":
		ds, err := cs.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}

		conds := make([]map[string]any, 0, len(ds.Status.Conditions))
		for _, c := range ds.Status.Conditions {
			conds = append(conds, map[string]any{
				"type":                 string(c.Type),
				"status":               string(c.Status),
				"reason":               c.Reason,
				"message":              c.Message,
				"last_transition_time": c.LastTransitionTime.Time.UTC().Format(time.RFC3339),
			})
		}

		status := map[string]any{
			"name":                     ds.Name,
			"namespace":                ds.Namespace,
			"desired_number_scheduled": ds.Status.DesiredNumberScheduled,
			"current_number_scheduled": ds.Status.CurrentNumberScheduled,
			"number_ready":             ds.Status.NumberReady,
			"updated_number_scheduled": ds.Status.UpdatedNumberScheduled,
			"number_available":         ds.Status.NumberAvailable,
			"conditions":               conds,
		}

		if ds.Status.CurrentNumberScheduled == ds.Status.DesiredNumberScheduled &&
			ds.Status.NumberReady == ds.Status.DesiredNumberScheduled &&
			ds.Status.UpdatedNumberScheduled == ds.Status.DesiredNumberScheduled {
			status["status"] = "complete"
			status["message"] = fmt.Sprintf(`daemon set "%s" successfully rolled out`, name)
		} else {
			status["status"] = "in progress"
			msg := fmt.Sprintf(`Waiting for daemon set "%s" rollout to finish: %d out of %d new pods have been updated...`,
				name, ds.Status.UpdatedNumberScheduled, ds.Status.DesiredNumberScheduled)
			if ds.Status.NumberReady < ds.Status.CurrentNumberScheduled {
				msg += fmt.Sprintf("\n%d of %d updated pods are ready...", ds.Status.NumberReady, ds.Status.CurrentNumberScheduled)
			}
			status["message"] = msg
		}

		b, _ := json.MarshalIndent(status, "", "  ")
		return textOKResult(string(b)), nil, nil

	case "statefulset":
		ss, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}

		replicas := ss.Status.Replicas
		ready := ss.Status.ReadyReplicas
		current := ss.Status.CurrentReplicas
		updated := ss.Status.UpdatedReplicas

		status := map[string]any{
			"name":             ss.Name,
			"namespace":        ss.Namespace,
			"replicas":         replicas,
			"ready_replicas":   ready,
			"current_replicas": current,
			"updated_replicas": updated,
			"current_revision": ss.Status.CurrentRevision,
			"update_revision":  ss.Status.UpdateRevision,
		}

		if ready == replicas && updated == replicas {
			status["status"] = "complete"
			status["message"] = fmt.Sprintf(`statefulset "%s" successfully rolled out`, name)
		} else {
			status["status"] = "in progress"
			msg := fmt.Sprintf(`Waiting for statefulset "%s" rollout to finish: %d out of %d new pods have been updated...`, name, updated, replicas)
			if ready < replicas {
				msg += fmt.Sprintf("\n%d of %d updated pods are ready...", ready, replicas)
			}
			status["message"] = msg
		}

		b, _ := json.MarshalIndent(status, "", "  ")
		return textOKResult(string(b)), nil, nil

	default:
		return textErrorResult(fmt.Sprintf("Error: resource type '%s' does not support rollout status", resourceType)), nil, nil
	}
}

// K8sRolloutHistory ports k8s_rollout_history(resource_type, name, namespace, revision)
func K8sRolloutHistory(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	resourceType, _ := args["resource_type"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	revision, _ := args["revision"].(string)

	if strings.TrimSpace(resourceType) == "" {
		return textErrorResult("resource_type is required"), nil, nil
	}
	if strings.TrimSpace(name) == "" {
		return textErrorResult("name is required"), nil, nil
	}
	if namespace == "" {
		namespace = "default"
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	switch strings.ToLower(resourceType) {
	case "deployment":
		dep, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}

		selector := labelsToSelector(dep.Spec.Selector.MatchLabels)
		rss, err := cs.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}

		// Sort by deployment.kubernetes.io/revision desc
		sort.Slice(rss.Items, func(i, j int) bool {
			return revisionNumber(&rss.Items[i]) > revisionNumber(&rss.Items[j])
		})

		type histEntry struct {
			Revision    string
			ReplicaSet  string
			Created     metav1.Time
			Containers  []map[string]string
			Replicas    *int32
			ChangeCause string
			Labels      map[string]string
			Annotations map[string]string
		}

		var history []histEntry

		for i := range rss.Items {
			rs := &rss.Items[i]
			rev := revisionString(rs)
			if revision != "" && revision != rev {
				continue
			}

			changeCause := ""
			if rs.Annotations != nil {
				changeCause = rs.Annotations["kubernetes.io/change-cause"]
			}

			containers := make([]map[string]string, 0, len(rs.Spec.Template.Spec.Containers))
			for _, c := range rs.Spec.Template.Spec.Containers {
				containers = append(containers, map[string]string{
					"name":  c.Name,
					"image": c.Image,
				})
			}

			he := histEntry{
				Revision:    rev,
				ReplicaSet:  rs.Name,
				Created:     rs.CreationTimestamp,
				Containers:  containers,
				Replicas:    rs.Spec.Replicas,
				ChangeCause: changeCause,
			}

			// Detailed view for a specific revision includes template labels/annotations
			if revision != "" && revision == rev {
				if rs.Spec.Template.Labels != nil {
					he.Labels = rs.Spec.Template.Labels
				}
				if rs.Spec.Template.Annotations != nil {
					he.Annotations = rs.Spec.Template.Annotations
				}
			}

			history = append(history, he)
		}

		if len(history) == 0 {
			return textOKResult("No rollout history found"), nil, nil
		}

		// Output like kubectl (as your python does)
		if revision != "" {
			h := history[0]
			var out strings.Builder
			out.WriteString(fmt.Sprintf("REVISION: %s\n", h.Revision))
			if h.ChangeCause != "" {
				out.WriteString(fmt.Sprintf("Change-Cause: %s\n", h.ChangeCause))
			}
			out.WriteString("Pod Template:\n")
			out.WriteString("  Labels:\n")
			for k, v := range h.Labels {
				out.WriteString(fmt.Sprintf("    %s: %s\n", k, v))
			}
			out.WriteString("  Containers:\n")
			for _, c := range h.Containers {
				out.WriteString(fmt.Sprintf("   %s:\n", c["name"]))
				out.WriteString(fmt.Sprintf("    Image: %s\n", c["image"]))
			}
			return textOKResult(out.String()), nil, nil
		}

		var out strings.Builder
		out.WriteString("REVISION  CHANGE-CAUSE\n")
		for _, h := range history {
			out.WriteString(fmt.Sprintf("%s        %s\n", h.Revision, h.ChangeCause))
		}
		return textOKResult(out.String()), nil, nil

	case "statefulset":
		ss, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		out := "StatefulSet revisions:\n"
		out += fmt.Sprintf("Current revision: %s\n", ss.Status.CurrentRevision)
		out += fmt.Sprintf("Update revision: %s\n", ss.Status.UpdateRevision)
		return textOKResult(out), nil, nil

	case "daemonset":
		ds, err := cs.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}

		selector := labelsToSelector(ds.Spec.Selector.MatchLabels)
		pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}

		revs := map[string]struct{}{}
		for _, p := range pods.Items {
			if p.Labels != nil {
				if h, ok := p.Labels["controller-revision-hash"]; ok && h != "" {
					revs[h] = struct{}{}
				}
			}
		}

		var out strings.Builder
		out.WriteString("DaemonSet revisions:\n")
		for r := range revs {
			out.WriteString(fmt.Sprintf("Revision: %s\n", r))
		}
		return textOKResult(out.String()), nil, nil

	default:
		return textErrorResult(fmt.Sprintf("Error: resource type '%s' history not available through API", resourceType)), nil, nil
	}
}

// K8sRolloutUndo ports k8s_rollout_undo(resource_type, name, namespace, to_revision)
func K8sRolloutUndo(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	resourceType, _ := args["resource_type"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	toRevision, _ := args["to_revision"].(string)

	if strings.TrimSpace(resourceType) == "" {
		return textErrorResult("resource_type is required"), nil, nil
	}
	if strings.TrimSpace(name) == "" {
		return textErrorResult("name is required"), nil, nil
	}
	if namespace == "" {
		namespace = "default"
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	switch strings.ToLower(resourceType) {
	case "deployment":
		dep, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}

		selector := labelsToSelector(dep.Spec.Selector.MatchLabels)
		rss, err := cs.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}

		var target *appsv1.ReplicaSet

		if toRevision != "" {
			for i := range rss.Items {
				rs := &rss.Items[i]
				if revisionString(rs) == toRevision {
					target = rs
					break
				}
			}
			if target == nil {
				return textErrorResult(fmt.Sprintf("Error: revision %s not found", toRevision)), nil, nil
			}

			dep.Spec.Template = target.Spec.Template
			_, err = cs.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
			if err != nil {
				return textErrorResult(formatK8sErr(err)), nil, nil
			}
			return textOKResult(fmt.Sprintf("Rollback to revision %s initiated successfully", toRevision)), nil, nil
		}

		// No toRevision => rollback to previous revision (2nd newest)
		sort.Slice(rss.Items, func(i, j int) bool {
			return revisionNumber(&rss.Items[i]) > revisionNumber(&rss.Items[j])
		})

		if len(rss.Items) < 2 {
			return textErrorResult("Error: No previous revision found for rollback"), nil, nil
		}
		target = &rss.Items[1]

		dep.Spec.Template = target.Spec.Template
		_, err = cs.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		return textOKResult("Rollback to previous revision initiated successfully"), nil, nil

	case "statefulset":
		// Matches python: patch updateStrategy.rollingUpdate.partition=0
		patch := []byte(`{"spec":{"updateStrategy":{"type":"RollingUpdate","rollingUpdate":{"partition":0}}}}`)
		_, err := cs.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		return textOKResult(fmt.Sprintf("Rollback of StatefulSet %s initiated successfully", name)), nil, nil

	case "daemonset":
		// Matches python: set restartedAt annotation (this "triggers a rollout")
		now := time.Now().UTC().Format(time.RFC3339Nano)
		patch := []byte(fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`, now))
		_, err := cs.AppsV1().DaemonSets(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		return textOKResult(fmt.Sprintf("Rollback of DaemonSet %s initiated successfully", name)), nil, nil

	default:
		return textErrorResult(fmt.Sprintf("Error: resource type '%s' rollback not available through API", resourceType)), nil, nil
	}
}

// K8sRolloutRestart ports k8s_rollout_restart(resource_type, name, namespace)
func K8sRolloutRestart(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	resourceType, _ := args["resource_type"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)

	if strings.TrimSpace(resourceType) == "" {
		return textErrorResult("resource_type is required"), nil, nil
	}
	if strings.TrimSpace(name) == "" {
		return textErrorResult("name is required"), nil, nil
	}
	if namespace == "" {
		namespace = "default"
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	patch := []byte(fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`, now))

	switch strings.ToLower(resourceType) {
	case "deployment":
		_, err := cs.AppsV1().Deployments(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		return textOKResult(fmt.Sprintf("Restart of %s/%s initiated successfully", resourceType, name)), nil, nil

	case "daemonset":
		_, err := cs.AppsV1().DaemonSets(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		return textOKResult(fmt.Sprintf("Restart of %s/%s initiated successfully", resourceType, name)), nil, nil

	case "statefulset":
		_, err := cs.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			return textErrorResult(formatK8sErr(err)), nil, nil
		}
		return textOKResult(fmt.Sprintf("Restart of %s/%s initiated successfully", resourceType, name)), nil, nil

	default:
		return textErrorResult(fmt.Sprintf("Error: resource type '%s' restart not available through API", resourceType)), nil, nil
	}
}

// K8sRolloutPause ports k8s_rollout_pause(resource_type, name, namespace)
func K8sRolloutPause(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	resourceType, _ := args["resource_type"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)

	if strings.TrimSpace(resourceType) == "" {
		return textErrorResult("resource_type is required"), nil, nil
	}
	if strings.TrimSpace(name) == "" {
		return textErrorResult("name is required"), nil, nil
	}
	if namespace == "" {
		namespace = "default"
	}

	if strings.ToLower(resourceType) != "deployment" {
		return textErrorResult(fmt.Sprintf("Error: resource type '%s' pause not available through API", resourceType)), nil, nil
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	patch := []byte(`{"spec":{"paused":true}}`)
	_, err = cs.AppsV1().Deployments(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return textErrorResult(formatK8sErr(err)), nil, nil
	}

	return textOKResult(fmt.Sprintf("Paused rollout of %s/%s successfully", resourceType, name)), nil, nil
}

// ---- helpers ----

func labelsToSelector(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func revisionString(rs *appsv1.ReplicaSet) string {
	if rs.Annotations == nil {
		return "unknown"
	}
	if v, ok := rs.Annotations["deployment.kubernetes.io/revision"]; ok && v != "" {
		return v
	}
	return "unknown"
}

func revisionNumber(rs *appsv1.ReplicaSet) int {
	if rs.Annotations == nil {
		return 0
	}
	v := rs.Annotations["deployment.kubernetes.io/revision"]
	if v == "" {
		return 0
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return i
}
