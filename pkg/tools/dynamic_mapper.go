package tools

import (
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

var (
	restMapperOnce sync.Once
	restMapper     meta.RESTMapper
	restMapperErr  error
)

// GetDynamicClient is a small exported wrapper used by create/apply.
// It relies on SetupClient() being called by the command handler (same pattern as other tools).
func GetDynamicClient() (dynamic.Interface, error) {
	return getDynamic()
}

// GetRESTMapper returns a cached RESTMapper built from discovery.
// This enables mapping GVK -> GVR for dynamic create/apply.
func GetRESTMapper() (meta.RESTMapper, error) {
	restMapperOnce.Do(func() {
		disc, err := getDiscovery()
		if err != nil {
			restMapperErr = err
			return
		}
		cache := memory.NewMemCacheClient(disc)
		restMapper = restmapper.NewDeferredDiscoveryRESTMapper(cache)
	})
	return restMapper, restMapperErr
}
