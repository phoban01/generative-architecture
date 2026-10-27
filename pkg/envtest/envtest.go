// Package envtest starts a local API server for tests with
// sigs.k8s.io/controller-runtime/pkg/envtest. It is a test helper: import
// it only from _test.go files.
//
// The API server and etcd binaries come from setup-envtest, which prints
// the directory to export as KUBEBUILDER_ASSETS (README.md has the
// versions):
//
//	go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.23 use 1.35.x --bin-dir bin/envtest -p path
//
// Without KUBEBUILDER_ASSETS, Start skips the test. With ENVTEST_REQUIRED
// set to a non-empty value, as in CI, Start fails the test instead, so a
// missing install never passes as a skip.
package envtest

import (
	"fmt"
	"os"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Environment variables Start reads.
const (
	// AssetsEnv names the directory of the kube-apiserver and etcd
	// binaries.
	AssetsEnv = "KUBEBUILDER_ASSETS"
	// RequiredEnv, when non-empty, turns a skip for missing assets into a
	// failure.
	RequiredEnv = "ENVTEST_REQUIRED"
)

// SkipMessage is the reason Start gives when it skips a test.
const SkipMessage = AssetsEnv + " is not set; skipping envtest. Install the assets with setup-envtest (see README.md) to run it"

// Start starts an API server with the CRDs found under crdPaths installed
// and returns a client for it and its rest config. The client uses scheme,
// or client-go's scheme when scheme is nil. The server stops when the test
// ends. A missing CRD path or a server that does not start fails the test.
func Start(t testing.TB, scheme *runtime.Scheme, crdPaths ...string) (client.Client, *rest.Config) {
	t.Helper()
	skip, err := decide(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Skip(SkipMessage)
	}
	if scheme == nil {
		scheme = clientgoscheme.Scheme
	}
	env := &envtest.Environment{
		CRDDirectoryPaths:     crdPaths,
		ErrorIfCRDPathMissing: true,
		Scheme:                scheme,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest: start: %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Errorf("envtest: stop: %v", err)
		}
	})
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("envtest: client: %v", err)
	}
	return c, cfg
}

// decide reports whether Start skips: it skips when the assets are not
// set, unless they are required, which is an error.
func decide(getenv func(string) string) (bool, error) {
	if getenv(AssetsEnv) != "" {
		return false, nil
	}
	if getenv(RequiredEnv) != "" {
		return false, fmt.Errorf("%s is set but %s is not: install the envtest assets with setup-envtest", RequiredEnv, AssetsEnv)
	}
	return true, nil
}
