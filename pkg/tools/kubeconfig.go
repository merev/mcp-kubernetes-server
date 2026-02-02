package tools

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// genKubeconfig generates a kubeconfig YAML string using the in-cluster
// service account token and CA cert, mirroring the Python gen_kubeconfig().
func genKubeconfig() (string, error) {
	tokenBytes, err := os.ReadFile("/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return "", fmt.Errorf("read serviceaccount token: %w", err)
	}
	token := string(bytesTrimSpace(tokenBytes))

	caBytes, err := os.ReadFile("/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return "", fmt.Errorf("read serviceaccount ca.crt: %w", err)
	}
	ca := string(bytesTrimSpace(caBytes))
	caB64 := base64.StdEncoding.EncodeToString([]byte(ca))

	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return "", fmt.Errorf("KUBERNETES_SERVICE_HOST or KUBERNETES_SERVICE_PORT not set")
	}

	// Same structure as the Python f-string kubeconfig.
	kubeconfig := fmt.Sprintf(`apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: %s
    server: https://%s:%s
  name: kube
contexts:
- context:
    cluster: kube
    user: kube
  name: kube
current-context: kube
kind: Config
users:
- name: kube
  user:
    token: %s
`, caB64, host, port, token)

	return kubeconfig, nil
}

// bytesTrimSpace is like strings.TrimSpace but for byte slices, without allocating.
func bytesTrimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && (b[start] == ' ' || b[start] == '\n' || b[start] == '\r' || b[start] == '\t') {
		start++
	}
	end := len(b)
	for end > start && (b[end-1] == ' ' || b[end-1] == '\n' || b[end-1] == '\r' || b[end-1] == '\t') {
		end--
	}
	return b[start:end]
}

// setupKubeconfig mirrors the Python setup_kubeconfig():
// - If KUBECONFIG env var is set and non-empty, do nothing.
// - If not running inside a Pod (no KUBERNETES_SERVICE_HOST), do nothing.
// - Else, ensure ~/.kube/config exists, writing a generated kubeconfig if absent.
func setupKubeconfig() error {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return nil
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		// Not running inside a Pod; nothing to do.
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	kubeconfigPath := filepath.Join(home, ".kube")
	kubeconfigFile := filepath.Join(kubeconfigPath, "config")

	// If kubeconfig already exists, no need to recreate it
	if _, err := os.Stat(kubeconfigFile); err == nil {
		return nil
	}

	if err := os.MkdirAll(kubeconfigPath, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", kubeconfigPath, err)
	}

	kcfg, err := genKubeconfig()
	if err != nil {
		return err
	}

	if err := os.WriteFile(kubeconfigFile, []byte(kcfg), 0o600); err != nil {
		return fmt.Errorf("write kubeconfig: %w", err)
	}
	return nil
}
