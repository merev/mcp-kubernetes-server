package tools

import (
	"bufio"
	"context"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// K8sLogs ports logs.py k8s_logs(...)
func K8sLogs(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	podName, _ := args["pod_name"].(string)
	if strings.TrimSpace(podName) == "" {
		return textErrorResult("pod_name is required"), nil, nil
	}

	container, _ := args["container"].(string)
	namespace, _ := args["namespace"].(string)
	if namespace == "" {
		namespace = "default"
	}

	previous := boolFromArgs(args, "previous", false)
	timestamps := boolFromArgs(args, "timestamps", false)
	follow := boolFromArgs(args, "follow", false)

	var tailLinesPtr *int64
	if tail, ok := intFromArgs(args, "tail"); ok {
		if tail > 0 {
			t := int64(tail)
			tailLinesPtr = &t
		}
	}

	var sinceSecondsPtr *int64
	if since, ok := args["since"].(string); ok && strings.TrimSpace(since) != "" {
		if ss := parseSinceSeconds(since); ss != nil {
			sinceSecondsPtr = ss
		}
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	// Get the pod so we can default container like Python
	pod, err := cs.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return textErrorResult(formatK8sErr(err)), nil, nil
	}

	// Default container to first container
	if container == "" {
		if pod.Spec.Containers != nil && len(pod.Spec.Containers) > 0 {
			container = pod.Spec.Containers[0].Name
		} else {
			return textErrorResult("Error: No containers found in pod"), nil, nil
		}
	}

	opts := &v1.PodLogOptions{
		Container:    container,
		Follow:       follow,
		Previous:     previous,
		Timestamps:   timestamps,
		TailLines:    tailLinesPtr,
		SinceSeconds: sinceSecondsPtr,
	}

	req := cs.CoreV1().Pods(namespace).GetLogs(podName, opts)

	// follow=false -> return full logs (like python)
	if !follow {
		b, err := req.DoRaw(ctx)
		if err != nil {
			// keep error formatting similar
			return textErrorResult(formatLogErr(err)), nil, nil
		}
		return textOKResult(string(b)), nil, nil
	}

	// follow=true -> stream logs, 1MB cap (like python)
	rc, err := req.Stream(ctx)
	if err != nil {
		return textErrorResult(formatLogErr(err)), nil, nil
	}
	defer rc.Close()

	const maxBytes = 1024 * 1024

	var sb strings.Builder
	sb.Grow(16 * 1024)

	reader := bufio.NewReader(rc)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			// Append and enforce cap
			if sb.Len()+len(line) > maxBytes {
				remaining := maxBytes - sb.Len()
				if remaining > 0 {
					sb.Write(line[:remaining])
				}
				sb.WriteString("\n... log output truncated ...\n")
				break
			}
			sb.Write(line)
		}

		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return textErrorResult("Error:\n" + readErr.Error()), nil, nil
		}
	}

	return textOKResult(sb.String()), nil, nil
}

func formatLogErr(err error) string {
	// Try to keep errors human-ish like python's ApiException str()
	// If it's a StatusError it will include useful details.
	if apierrors.IsNotFound(err) {
		return "Error: " + err.Error()
	}
	return "Error: " + err.Error()
}

var sinceRe = regexp.MustCompile(`^(\d+)([smhd])$`)

// parseSinceSeconds ports logs.py _parse_since()
// - relative duration: 5s, 2m, 3h, 1d
// - ISO timestamp (e.g. 2025-01-01T10:00:00Z) -> seconds from then to now
func parseSinceSeconds(since string) *int64 {
	since = strings.TrimSpace(since)
	if since == "" {
		return nil
	}

	// relative duration
	if m := sinceRe.FindStringSubmatch(since); len(m) == 3 {
		val, err := strconv.Atoi(m[1])
		if err != nil || val < 0 {
			return nil
		}
		unit := m[2]
		secs := int64(val)
		switch unit {
		case "s":
			// noop
		case "m":
			secs *= 60
		case "h":
			secs *= 60 * 60
		case "d":
			secs *= 60 * 60 * 24
		default:
			return nil
		}
		return &secs
	}

	// absolute timestamp (ISO8601-ish). Python used fromisoformat after replacing Z with +00:00.
	// In Go, try RFC3339, then RFC3339Nano.
	ts := since
	if strings.HasSuffix(ts, "Z") {
		// time.Parse handles Z already, keep it as is
	}
	var t time.Time
	var err error
	t, err = time.Parse(time.RFC3339, ts)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return nil
		}
	}

	now := time.Now().UTC()
	diff := now.Sub(t)
	secs := int64(diff.Seconds())
	if secs < 0 {
		secs = 0
	}
	return &secs
}
