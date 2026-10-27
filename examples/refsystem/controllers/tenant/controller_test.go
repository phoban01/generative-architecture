package tenant

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/phoban01/generative-architecture/examples/refsystem/api/v1alpha1"
	"github.com/phoban01/generative-architecture/examples/refsystem/internal/extractcheck"
)

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(&v1alpha1.Tenant{}).Build()
}

func reconcileTenant(t *testing.T, c client.Client) {
	t.Helper()
	r := &TenantReconciler{Client: c}
	req := reconcile.Request{NamespacedName: client.ObjectKey{Namespace: "default", Name: "acme"}}
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res != (reconcile.Result{}) {
		t.Fatalf("unexpected result %+v", res)
	}
}

func tenant() *v1alpha1.Tenant {
	return &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "acme"}, Spec: v1alpha1.TenantSpec{Quota: "small"}}
}

func TestReconcileCreatesNamespaceAndQuota(t *testing.T) {
	c := newClient(t, tenant())
	reconcileTenant(t, c)
	ctx := context.Background()

	if err := c.Get(ctx, client.ObjectKey{Name: "acme"}, &corev1.Namespace{}); err != nil {
		t.Fatalf("namespace: %v", err)
	}
	quota := &corev1.ResourceQuota{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: "acme", Name: QuotaName}, quota); err != nil {
		t.Fatalf("quota: %v", err)
	}
	if quota.Labels["profile"] != "small" {
		t.Fatalf("quota labels %v", quota.Labels)
	}
	got := &v1alpha1.Tenant{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "acme"}, got); err != nil {
		t.Fatal(err)
	}
	if !got.Status.NamespaceReady {
		t.Fatal("namespaceReady not set")
	}
}

func TestReconcileUpdatesStatusWhenObjectsExist(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Namespace: "acme", Name: QuotaName, Labels: map[string]string{"profile": "large"}}}
	c := newClient(t, tenant(), ns, quota)
	reconcileTenant(t, c)
	ctx := context.Background()

	// Existing objects are left as they are.
	got := &corev1.ResourceQuota{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: "acme", Name: QuotaName}, got); err != nil {
		t.Fatal(err)
	}
	if got.Labels["profile"] != "large" {
		t.Fatalf("existing quota changed: %v", got.Labels)
	}
	tn := &v1alpha1.Tenant{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "acme"}, tn); err != nil {
		t.Fatal(err)
	}
	if !tn.Status.NamespaceReady {
		t.Fatal("namespaceReady not set")
	}

	// A second pass with a ready tenant writes nothing.
	version := tn.ResourceVersion
	reconcileTenant(t, c)
	if err := c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "acme"}, tn); err != nil {
		t.Fatal(err)
	}
	if tn.ResourceVersion != version {
		t.Fatalf("ready tenant was written again: %s -> %s", version, tn.ResourceVersion)
	}
}

func TestReconcileIgnoresMissingTenant(t *testing.T) {
	reconcileTenant(t, newClient(t))
}

func TestExtractsWithoutUnsupportedConstructs(t *testing.T) {
	res, err := extractcheck.Run("./examples/refsystem/controllers/tenant", "TenantReconciler")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Diagnostics {
		t.Errorf("unsupported: %v", d)
	}
	if len(res.Controllers) != 1 || res.Controllers[0].Name != "TenantReconciler" {
		t.Fatalf("controllers %+v", res.Controllers)
	}
	if res.Program == nil || len(res.Program.Nodes) == 0 {
		t.Fatal("Reconcile not extracted")
	}
	var kinds []string
	for _, rule := range res.Watches.Rules {
		kinds = append(kinds, rule.Resource.Kind+"/"+rule.Mapping)
	}
	if want := []string{"Tenant/self", "Namespace/owner"}; !equal(kinds, want) {
		t.Fatalf("watches %v, want %v", kinds, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
