package tools

import (
	"context"
	"fmt"
	"os"

	extclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	kubeClient      *kubernetes.Clientset
	kubeConfig      *rest.Config
	dynClient       dynamic.Interface
	discClient      discovery.DiscoveryInterface
	apiExtClientset *extclientset.Clientset
)

// SetupClient mirrors the Python setup_client():
// - best-effort setupKubeconfig() to generate ~/.kube/config when running in a Pod
// - try in-cluster config
// - fall back to kubeconfig (KUBECONFIG or ~/.kube/config)
func SetupClient(ctx context.Context) error {
	_ = ctx

	_ = setupKubeconfig()

	if kubeClient != nil && kubeConfig != nil && dynClient != nil && discClient != nil && apiExtClientset != nil {
		return nil
	}

	// 1) Try in-cluster
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// 2) Fall back to kubeconfig
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		if envKube := os.Getenv("KUBECONFIG"); envKube != "" {
			loadingRules.ExplicitPath = envKube
		}
		overrides := &clientcmd.ConfigOverrides{}
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			loadingRules,
			overrides,
		).ClientConfig()
		if err != nil {
			return fmt.Errorf("build Kubernetes client config: %w", err)
		}
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create Kubernetes clientset: %w", err)
	}

	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create Kubernetes dynamic client: %w", err)
	}

	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create Kubernetes discovery client: %w", err)
	}

	extcs, err := extclientset.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create Kubernetes apiextensions clientset: %w", err)
	}

	kubeConfig = cfg
	kubeClient = cs
	dynClient = dc
	discClient = disc
	apiExtClientset = extcs

	return nil
}

func getClient() (*kubernetes.Clientset, error) {
	if kubeClient == nil {
		return nil, fmt.Errorf("Kubernetes client is not initialized")
	}
	return kubeClient, nil
}

func getDiscovery() (discovery.DiscoveryInterface, error) {
	if discClient == nil {
		return nil, fmt.Errorf("Kubernetes discovery client is not initialized")
	}
	return discClient, nil
}

func getDynamic() (dynamic.Interface, error) {
	if dynClient == nil {
		return nil, fmt.Errorf("Kubernetes dynamic client is not initialized")
	}
	return dynClient, nil
}

func getAPIExtensions() (*extclientset.Clientset, error) {
	if apiExtClientset == nil {
		return nil, fmt.Errorf("Kubernetes apiextensions clientset is not initialized")
	}
	return apiExtClientset, nil
}

func getRestConfig() (*rest.Config, error) {
	if kubeConfig == nil {
		return nil, fmt.Errorf("Kubernetes REST config is not initialized")
	}
	return kubeConfig, nil
}
