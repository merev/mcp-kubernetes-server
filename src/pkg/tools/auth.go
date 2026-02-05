package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
)

// K8sAuthWhoami mirrors auth.py k8s_auth_whoami():
// - reads current context from kubeconfig
// - includes username/client_certificate/token-present hints when available
func K8sAuthWhoami(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	_ = ctx

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if envKube := os.Getenv("KUBECONFIG"); envKube != "" {
		loadingRules.ExplicitPath = envKube
	}
	overrides := &clientcmd.ConfigOverrides{}

	raw, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).RawConfig()
	if err != nil {
		return textErrorResult("Error:\n" + err.Error()), nil, nil
	}

	currentName := raw.CurrentContext
	if currentName == "" {
		// Kubernetes python kube_config.list_kube_config_contexts()[1] would error similarly;
		// we return a meaningful message.
		return textErrorResult("Error:\nno current context set in kubeconfig"), nil, nil
	}

	ctxObj, ok := raw.Contexts[currentName]
	if !ok || ctxObj == nil {
		return textErrorResult(fmt.Sprintf("Error:\ncurrent context '%s' not found in kubeconfig", currentName)), nil, nil
	}

	userInfo := map[string]any{}

	// Pull auth hints from the user entry (similar spirit to python client.Configuration fields)
	if ai, ok := raw.AuthInfos[ctxObj.AuthInfo]; ok && ai != nil {
		if ai.Username != "" {
			userInfo["username"] = ai.Username
		}

		// Token present? (token or tokenFile or exec plugin)
		if ai.Token != "" || ai.TokenFile != "" || ai.Exec != nil {
			userInfo["token"] = "present"
		}

		// Cert present?
		if ai.ClientCertificate != "" {
			userInfo["client_certificate"] = ai.ClientCertificate
		} else if len(ai.ClientCertificateData) > 0 {
			userInfo["client_certificate"] = "present"
		}
	}

	userInfo["context"] = map[string]any{
		"name":    currentName,
		"cluster": ctxObj.Cluster,
		"user":    ctxObj.AuthInfo,
	}

	b, _ := json.MarshalIndent(userInfo, "", "  ")
	return textOKResult(string(b)), nil, nil
}

// K8sAuthCanI mirrors auth.py k8s_auth_can_i(verb, resource, subresource, namespace, name)
func K8sAuthCanI(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	verb, _ := args["verb"].(string)
	resource, _ := args["resource"].(string)
	subresource, _ := args["subresource"].(string)
	namespace, _ := args["namespace"].(string)
	name, _ := args["name"].(string)

	if verb == "" {
		return textErrorResult("verb is required"), nil, nil
	}
	if resource == "" {
		return textErrorResult("resource is required"), nil, nil
	}

	// Python default
	if namespace == "" {
		namespace = "default"
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	sar := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace:   namespace,
				Verb:        verb,
				Resource:    resource,
				Subresource: emptyToNilString(subresource),
				Name:        emptyToNilString(name),
			},
		},
	}

	resp, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
	if err != nil {
		return textErrorResult("Error:\n" + err.Error()), nil, nil
	}

	out := map[string]any{
		"allowed": resp.Status.Allowed,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return textOKResult(string(b)), nil, nil
}

func emptyToNilString(s string) string {
	// In k8s Go types, empty string is fine; this helper just keeps intent explicit.
	return s
}
