package tools

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- Generic glue (matches go-sdk v1.2.0) ----

// AddTool binds a tool name/description to a typed handler.
// We use In=map[string]any and Out=any for now to avoid having to define schemas
// until we port each Python module.
func AddTool(srv *mcp.Server, name, desc string, h mcp.ToolHandlerFor[map[string]any, any]) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        name,
		Description: desc,
	}, h)
}

var ErrNotImplemented = errors.New("not implemented yet (waiting for python module to port)")

func notImplementedTool(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: ErrNotImplemented.Error()}},
	}, nil, nil
}

// ---- Tool stubs (we'll replace each with real logic) ----

var (
	K8sAuthWhoAmI    mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sDelete        mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sPatch         mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sLabel         mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sAnnotate      mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sExpose        mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sRun           mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sExecCommand   mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sScale         mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sAutoscale     mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sTaint         mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sUntaint       mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
	K8sRolloutResume mcp.ToolHandlerFor[map[string]any, any] = notImplementedTool
)

// ---- kubectl/helm tools ----
// For these, we DO define a typed input so schema inference produces a nice contract.

type CommandArgs struct {
	Command string `json:"command" jsonschema:"The full command line to execute (e.g. 'get pods -A')"`
}

// RegisterKubectlTool matches your python logic: blocks write/delete subcommands depending on flags.
func RegisterKubectlTool(srv *mcp.Server, disableWrite, disableDelete bool) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "kubectl",
		Description: "Run a kubectl command and return the output",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args CommandArgs) (*mcp.CallToolResult, any, error) {
		cmdStr := args.Command

		writeOps := map[string]bool{
			"create": true, "apply": true, "edit": true, "patch": true, "replace": true,
			"scale": true, "autoscale": true, "label": true, "annotate": true,
			"set": true, "rollout": true, "expose": true, "run": true,
			"cordon": true, "delete": true, "uncordon": true, "drain": true,
			"taint": true, "untaint": true, "cp": true, "exec": true, "port-forward": true,
		}
		deleteOps := map[string]bool{"delete": true}

		sub := firstSubcommand(cmdStr, "kubectl")
		if sub != "" {
			if disableDelete && deleteOps[sub] {
				return textErrorResult("Error: Write operations are not allowed. Cannot execute kubectl delete command."), nil, nil
			}
			if disableWrite && writeOps[sub] {
				return textErrorResult("Error: Write operations are not allowed. Cannot execute kubectl " + sub + " command."), nil, nil
			}
		}

		out, err := runCommand("kubectl", cmdStr)
		if err != nil {
			return textErrorResult(out), nil, nil
		}
		return textOKResult(out), nil, nil
	})
}

func RegisterHelmTool(srv *mcp.Server, disableWrite bool) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "helm",
		Description: "Run a helm command and return the output",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args CommandArgs) (*mcp.CallToolResult, any, error) {
		cmdStr := args.Command

		writeOps := map[string]bool{
			"install": true, "upgrade": true, "uninstall": true, "rollback": true,
			"push": true, "create": true, "package": true,
			"repo add": true, "repo update": true, "repo remove": true,
			"dependency update": true,
			"plugin install":    true, "plugin uninstall": true,
		}

		if disableWrite {
			sub1, sub2 := firstTwoSubcommands(cmdStr, "helm")
			if sub1 != "" && writeOps[sub1] {
				return textErrorResult("Error: Write operations are not allowed. Cannot execute helm " + sub1 + " command."), nil, nil
			}
			if sub1 != "" && sub2 != "" && writeOps[sub1+" "+sub2] {
				return textErrorResult("Error: Write operations are not allowed. Cannot execute helm " + sub1 + " " + sub2 + " command."), nil, nil
			}
		}

		out, err := runCommand("helm", cmdStr)
		if err != nil {
			return textErrorResult(out), nil, nil
		}
		return textOKResult(out), nil, nil
	})
}

// ---- helpers ----

func textOKResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: false,
		Content: []mcp.Content{&mcp.TextContent{Text: s}},
	}
}

func textErrorResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: s}},
	}
}

func firstSubcommand(command, bin string) string {
	parts := strings.Fields(strings.TrimSpace(command))
	if len(parts) == 0 {
		return ""
	}
	if parts[0] == bin {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func firstTwoSubcommands(command, bin string) (string, string) {
	parts := strings.Fields(strings.TrimSpace(command))
	if len(parts) == 0 {
		return "", ""
	}
	if parts[0] == bin {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

func runCommand(binary string, full string) (string, error) {
	parts := strings.Fields(full)
	if len(parts) > 0 && parts[0] == binary {
		parts = parts[1:]
	}
	cmd := exec.Command(binary, parts...)
	b, err := cmd.CombinedOutput()
	return string(b), err
}
