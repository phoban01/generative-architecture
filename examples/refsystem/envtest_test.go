package refsystem_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/phoban01/generative-architecture/examples/refsystem/api/v1alpha1"
	"github.com/phoban01/generative-architecture/pkg/envtest"
)

// TestEnvtestCreatesTenant installs the reference system's three CRDs in a
// local API server and creates one Tenant. It skips without
// KUBEBUILDER_ASSETS; CI installs the assets and requires it to run.
func TestEnvtestCreatesTenant(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c, _ := envtest.Start(t, scheme, filepath.Join("config", "crd"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tenant := &v1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "default"},
		Spec:       v1alpha1.TenantSpec{Quota: "small"},
	}
	if err := c.Create(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	var got v1alpha1.Tenant
	if err := c.Get(ctx, client.ObjectKeyFromObject(tenant), &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Quota != "small" || got.UID == "" {
		t.Fatalf("stored tenant %+v", got)
	}
}
