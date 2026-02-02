package server

import (
	"context"
	"flag"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"log"
	"net/http"

	"github.com/merev/mcp-kubernetes-server/pkg/tools"
)

type Options struct {
	DisableKubectl bool
	DisableHelm    bool
	DisableWrite   bool
	DisableDelete  bool
	Transport      string
	Host           string
	Port           int
}

func Run() error {
	opts := parseFlags()

	// Implementation metadata (similar to FastMCP("mcp-kubernetes-server"))
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "mcp-kubernetes-server",
		Version: "dev",
	}, nil)

	// Equivalent to setup_client() in Python.
	// We'll implement this once you provide kubeclient.py (config loading, in-cluster, etc).
	if err := tools.SetupClient(context.Background()); err != nil {
		return fmt.Errorf("setup k8s client: %w", err)
	}

	registerReadTools(srv)

	if !opts.DisableWrite {
		registerWriteTools(srv)
	}
	if !opts.DisableDelete {
		registerDeleteTools(srv)
	}

	if !opts.DisableKubectl {
		tools.RegisterKubectlTool(srv, opts.DisableWrite, opts.DisableDelete)
	}
	if !opts.DisableHelm {
		tools.RegisterHelmTool(srv, opts.DisableWrite)
	}

	switch opts.Transport {
	case "stdio":
		// Run the server over stdin/stdout, until the client disconnects.
		return srv.Run(context.Background(), &mcp.StdioTransport{})

	case "sse", "streamable-http":
		// In the Go SDK, Streamable HTTP is exposed via an HTTP handler.
		// This is the closest match to your Python "sse" and "streamable-http" options.
		// (We keep both flags for compatibility.)
		addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)

		handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
			// You can decide later if you want per-request server instances.
			// For now: reuse one server.
			return srv
		}, nil)

		log.Printf("MCP Streamable HTTP listening on http://%s", addr)
		return http.ListenAndServe(addr, handler)

	default:
		return fmt.Errorf("unsupported transport: %q (expected stdio|sse|streamable-http)", opts.Transport)
	}
}

func parseFlags() Options {
	var opts Options
	flag.BoolVar(&opts.DisableKubectl, "disable-kubectl", false, "Disable kubectl command execution")
	flag.BoolVar(&opts.DisableHelm, "disable-helm", false, "Disable helm command execution")
	flag.BoolVar(&opts.DisableWrite, "disable-write", false, "Disable write operations")
	flag.BoolVar(&opts.DisableDelete, "disable-delete", false, "Disable delete operations")
	flag.StringVar(&opts.Transport, "transport", "stdio", "Transport mechanism to use (stdio or sse or streamable-http)")
	flag.StringVar(&opts.Host, "host", "127.0.0.1", "Host to use for sse or streamable-http server")
	flag.IntVar(&opts.Port, "port", 8000, "Port to use for sse or streamable-http server")
	flag.Parse()
	return opts
}

func registerReadTools(srv *mcp.Server) {
	tools.AddTool(srv, "k8s_apis", "List Kubernetes APIs", tools.K8sApis)
	tools.AddTool(srv, "k8s_crds", "List Kubernetes CRDs", tools.K8sCrds)
	tools.AddTool(srv, "k8s_get", "Get Kubernetes resources", tools.K8sGet)
	tools.AddTool(srv, "k8s_rollout_status", "Get rollout status", tools.K8sRolloutStatus)
	tools.AddTool(srv, "k8s_rollout_history", "Get rollout history", tools.K8sRolloutHistory)
	tools.AddTool(srv, "k8s_top_nodes", "Top nodes", tools.K8sTopNodes)
	tools.AddTool(srv, "k8s_top_pods", "Top pods", tools.K8sTopPods)
	tools.AddTool(srv, "k8s_describe", "Describe Kubernetes resources", tools.K8sDescribe)
	tools.AddTool(srv, "k8s_logs", "Get logs", tools.K8sLogs)
	tools.AddTool(srv, "k8s_events", "Get events", tools.K8sEvents)
	tools.AddTool(srv, "k8s_auth_can_i", "Auth can-i", tools.K8sAuthCanI)
	tools.AddTool(srv, "k8s_auth_whoami", "Auth whoami", tools.K8sAuthWhoAmI)
}

func registerWriteTools(srv *mcp.Server) {
	tools.AddTool(srv, "k8s_create", "Create resources", tools.K8sCreate)
	tools.AddTool(srv, "k8s_expose", "Expose resources", tools.K8sExpose)
	tools.AddTool(srv, "k8s_run", "Run resources", tools.K8sRun)
	tools.AddTool(srv, "k8s_set_resources", "Set resources", tools.K8sSetResources)
	tools.AddTool(srv, "k8s_set_image", "Set image", tools.K8sSetImage)
	tools.AddTool(srv, "k8s_set_env", "Set env", tools.K8sSetEnv)

	tools.AddTool(srv, "k8s_rollout_undo", "Rollout undo", tools.K8sRolloutUndo)
	tools.AddTool(srv, "k8s_rollout_restart", "Rollout restart", tools.K8sRolloutRestart)
	tools.AddTool(srv, "k8s_rollout_pause", "Rollout pause", tools.K8sRolloutPause)
	tools.AddTool(srv, "k8s_rollout_resume", "Rollout resume", tools.K8sRolloutResume)

	tools.AddTool(srv, "k8s_scale", "Scale resources", tools.K8sScale)
	tools.AddTool(srv, "k8s_autoscale", "Autoscale resources", tools.K8sAutoscale)
	tools.AddTool(srv, "k8s_cordon", "Cordon node", tools.K8sCordon)
	tools.AddTool(srv, "k8s_uncordon", "Uncordon node", tools.K8sUncordon)
	tools.AddTool(srv, "k8s_drain", "Drain node", tools.K8sDrain)

	tools.AddTool(srv, "k8s_taint", "Taint node", tools.K8sTaint)
	tools.AddTool(srv, "k8s_untaint", "Untaint node", tools.K8sUntaint)

	tools.AddTool(srv, "k8s_exec_command", "Exec command", tools.K8sExecCommand)
	tools.AddTool(srv, "k8s_port_forward", "Port-forward", tools.K8sPortForward)
	tools.AddTool(srv, "k8s_cp", "Copy files", tools.K8sCp)

	tools.AddTool(srv, "k8s_apply", "Apply manifests", tools.K8sApply)
	tools.AddTool(srv, "k8s_patch", "Patch resources", tools.K8sPatch)
	tools.AddTool(srv, "k8s_label", "Label resources", tools.K8sLabel)
	tools.AddTool(srv, "k8s_annotate", "Annotate resources", tools.K8sAnnotate)
}

func registerDeleteTools(srv *mcp.Server) {
	tools.AddTool(srv, "k8s_delete", "Delete resources", tools.K8sDelete)
}
