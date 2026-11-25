package boundary_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/phoban01/generative-architecture/pkg/boundary"
	"github.com/phoban01/generative-architecture/pkg/envtest"
)

// TestEnvtestServerSideApplyFieldManager is the envtest conformance hook of
// the Kustomization boundary's serverSideApply assumption: an object the
// controller applies with server-side apply records the field manager
// kustomize-controller as the owner of the applied fields, and a second
// manager that applies the same field without force gets a conflict. The
// API server, not Flux, provides that behaviour, so a local API server
// checks it. The hook's reference names this test.
func TestEnvtestServerSideApplyFieldManager(t *testing.T) {
	b := loadLibrary(t, "flux/kustomization.yaml")
	const hook = "pkg/boundary.TestEnvtestServerSideApplyFieldManager"
	for _, a := range b.Spec.Assumptions {
		if a.ID == "serverSideApply" && (a.Conformance.Kind != boundary.ConformanceEnvtest || a.Conformance.Reference != hook) {
			t.Fatalf("serverSideApply hook %+v, want envtest %s", a.Conformance, hook)
		}
	}
	c, _ := envtest.Start(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	applied := &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "applied"},
		Data:       map[string]string{"key": "from-kustomize"},
	}
	if err := c.Patch(ctx, applied, client.Apply, client.FieldOwner("kustomize-controller"), client.ForceOwnership); err != nil {
		t.Fatal(err)
	}
	var stored corev1.ConfigMap
	if err := c.Get(ctx, client.ObjectKeyFromObject(applied), &stored); err != nil {
		t.Fatal(err)
	}
	owned := false
	for _, m := range stored.ManagedFields {
		if m.Manager == "kustomize-controller" && m.Operation == metav1.ManagedFieldsOperationApply {
			owned = true
		}
	}
	if !owned {
		t.Fatalf("managed fields do not record kustomize-controller as an applier: %+v", stored.ManagedFields)
	}
	other := &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "applied"},
		Data:       map[string]string{"key": "from-other"},
	}
	err := c.Patch(ctx, other, client.Apply, client.FieldOwner("other"))
	if !apierrors.IsConflict(err) {
		t.Fatalf("a second manager applying the same field without force got %v, want a conflict", err)
	}
}
