package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type portForwardPortInfo struct {
	LocalPort  string `json:"local_port"`
	RemotePort string `json:"remote_port"`
	Address    string `json:"address"`
	URL        string `json:"url"`
}

type portForwardResult struct {
	Status       string                `json:"status"`
	PID          int                   `json:"pid"`
	ResourceType string                `json:"resource_type"`
	ResourceName string                `json:"resource_name"`
	Namespace    string                `json:"namespace"`
	Ports        []portForwardPortInfo `json:"ports"`
	Message      string                `json:"message"`
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// K8sPortForward forwards one or more local ports to a target resource using kubectl port-forward.
func K8sPortForward(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	// Match python defaults
	resourceType := getStringArg(args, "resource_type", "resourceType")
	name := getStringArg(args, "name")
	namespace := getStringArg(args, "namespace")
	address := getStringArg(args, "address")

	if strings.TrimSpace(namespace) == "" {
		namespace = "default"
	}
	if strings.TrimSpace(address) == "" {
		address = "127.0.0.1"
	}

	ports, err := parsePortsArg(args["ports"])
	if err != nil {
		return textErrorResult(fmt.Sprintf("Error: invalid ports: %v", err)), nil, nil
	}
	if resourceType == "" || name == "" {
		return textErrorResult("Error: resource_type and name are required"), nil, nil
	}
	if len(ports) == 0 {
		return textErrorResult("Error: ports is required"), nil, nil
	}

	// Build kubectl command (same as python)
	cmdArgs := []string{"port-forward", fmt.Sprintf("%s/%s", resourceType, name), "-n", namespace}
	if address != "" {
		cmdArgs = append(cmdArgs, "--address", address)
	}
	cmdArgs = append(cmdArgs, ports...)

	cmd := exec.CommandContext(ctx, "kubectl", cmdArgs...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return textErrorResult(fmt.Sprintf("Error: failed to capture stdout: %v", err)), nil, nil
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return textErrorResult(fmt.Sprintf("Error: failed to capture stderr: %v", err)), nil, nil
	}

	var stdoutBuf, stderrBuf safeBuffer

	if err := cmd.Start(); err != nil {
		return textErrorResult(fmt.Sprintf("Error: Port-forward failed to start: %v", err)), nil, nil
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	// Drain stdout/stderr like the python thread does.
	go func() { _, _ = io.Copy(&stdoutBuf, stdoutPipe) }()
	go func() { _, _ = io.Copy(&stderrBuf, stderrPipe) }()

	// Wait in background and just keep buffers filled.
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()

	// Wait ~1s like python, to detect immediate failure.
	select {
	case err := <-exitCh:
		// Process exited quickly -> treat as failed to start
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = strings.TrimSpace(stdoutBuf.String())
		}
		if msg == "" && err != nil {
			msg = err.Error()
		}
		if msg == "" {
			msg = "port-forward exited immediately"
		}
		return textErrorResult(fmt.Sprintf("Error: Port-forward failed to start: %s", msg)), nil, nil
	case <-time.After(1 * time.Second):
		// still running
	}

	// Format port info like python
	portInfo := make([]portForwardPortInfo, 0, len(ports))
	for _, p := range ports {
		local, remote := splitPortSpec(p)
		portInfo = append(portInfo, portForwardPortInfo{
			LocalPort:  local,
			RemotePort: remote,
			Address:    address,
			URL:        fmt.Sprintf("http://%s:%s", address, local),
		})
	}

	out := portForwardResult{
		Status:       "running",
		PID:          pid,
		ResourceType: resourceType,
		ResourceName: name,
		Namespace:    namespace,
		Ports:        portInfo,
		Message:      fmt.Sprintf("Port-forward to %s/%s started. Use Ctrl+C to stop.", resourceType, name),
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return textErrorResult(fmt.Sprintf("Error: %v", err)), nil, nil
	}
	return textOKResult(string(b)), nil, nil
}

func parsePortsArg(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, nil
		}
		return []string{s}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, it := range t {
			s, ok := it.(string)
			if !ok {
				return nil, fmt.Errorf("ports array must contain strings")
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		return out, nil
	case []string:
		out := make([]string, 0, len(t))
		for _, s := range t {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("ports must be a string or list of strings")
	}
}

// "8080:80" => ("8080","80"), "8080" => ("8080","8080")
func splitPortSpec(s string) (local string, remote string) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) == 1 {
		return parts[0], parts[0]
	}
	// kubectl also supports "LOCAL:REMOTE" for pod port-forward; keep it simple
	return parts[0], parts[1]
}
