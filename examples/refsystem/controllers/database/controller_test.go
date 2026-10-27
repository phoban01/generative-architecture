package database

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

	"generative-architecture/examples/refsystem/api/v1alpha1"
	"generative-architecture/examples/refsystem/internal/extractcheck"
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
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(&v1alpha1.Database{}, &v1alpha1.Tenant{}).Build()
}

func tenant(ready bool) *v1alpha1.Tenant {
	return &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "acme"}, Status: v1alpha1.TenantStatus{NamespaceReady: ready}}
}

func database() *v1alpha1.Database {
	return &v1alpha1.Database{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "orders", Labels: map[string]string{TenantLabel: "acme"}},
		Spec:       v1alpha1.DatabaseSpec{Tenant: "acme", Size: "small"},
	}
}

func reconcileDatabase(t *testing.T, c client.Client) *v1alpha1.Database {
	t.Helper()
	r := &DatabaseReconciler{Client: c}
	key := client.ObjectKey{Namespace: "default", Name: "orders"}
	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key})
	if err != nil {
		t.Fatal(err)
	}
	if res != (reconcile.Result{}) {
		t.Fatalf("unexpected result %+v", res)
	}
	got := &v1alpha1.Database{}
	if err := c.Get(context.Background(), key, got); err != nil {
		return nil
	}
	return got
}

func TestReconcileWaitsForTenant(t *testing.T) {
	for name, objs := range map[string][]client.Object{
		"missing": {database()},
		"unready": {database(), tenant(false)},
	} {
		t.Run(name, func(t *testing.T) {
			c := newClient(t, objs...)
			got := reconcileDatabase(t, c)
			if got.Status.Ready {
				t.Fatal("database marked ready before its tenant")
			}
			secrets := &corev1.SecretList{}
			if err := c.List(context.Background(), secrets); err != nil {
				t.Fatal(err)
			}
			if len(secrets.Items) != 0 {
				t.Fatalf("secrets created early: %+v", secrets.Items)
			}
		})
	}
}

func TestReconcileCreatesSecret(t *testing.T) {
	c := newClient(t, database(), tenant(true))
	got := reconcileDatabase(t, c)
	if !got.Status.Ready || got.Status.SecretName != "orders" {
		t.Fatalf("status %+v", got.Status)
	}
	secret := &corev1.Secret{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "acme", Name: "orders"}, secret); err != nil {
		t.Fatal(err)
	}
	if secret.StringData["username"] != "orders" {
		t.Fatalf("secret data %v", secret.StringData)
	}

	// A second pass with a ready database writes nothing.
	again := reconcileDatabase(t, c)
	if again.ResourceVersion != got.ResourceVersion {
		t.Fatalf("ready database was written again: %s -> %s", got.ResourceVersion, again.ResourceVersion)
	}
}

func TestReconcileKeepsExistingSecret(t *testing.T) {
	existing := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "acme", Name: "orders"}, StringData: map[string]string{"username": "admin"}}
	c := newClient(t, database(), tenant(true), existing)
	got := reconcileDatabase(t, c)
	if !got.Status.Ready {
		t.Fatalf("status %+v", got.Status)
	}
	secret := &corev1.Secret{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "acme", Name: "orders"}, secret); err != nil {
		t.Fatal(err)
	}
	if secret.StringData["username"] != "admin" {
		t.Fatalf("existing secret changed: %v", secret.StringData)
	}
}

func TestTenantToDatabases(t *testing.T) {
	other := database()
	other.Name, other.Labels = "unrelated", map[string]string{TenantLabel: "other"}
	r := &DatabaseReconciler{Client: newClient(t, database(), other)}
	got := r.tenantToDatabases(context.Background(), tenant(true))
	want := []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: "default", Name: "orders"}}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("requests %v, want %v", got, want)
	}
}

// TestExtraction checks that discovery and Reconcile extract with no
// unsupported construct. The Tenant watch needs a map function that lists
// Databases, which watchgen does not read yet: it accepts only a package
// function returning one key from a field of the watched object. The test
// pins that one diagnostic so it fails when the gap closes or a new one
// appears.
func TestExtraction(t *testing.T) {
	res, err := extractcheck.Run("./examples/refsystem/controllers/database", "DatabaseReconciler")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Controllers) != 1 || res.Controllers[0].Name != "DatabaseReconciler" {
		t.Fatalf("controllers %+v", res.Controllers)
	}
	if res.Program == nil || len(res.Program.Nodes) == 0 {
		t.Fatal("Reconcile not extracted")
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("diagnostics %v, want only the list-based map function", res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.Construct != "handler" || d.Source != "r.tenantToDatabases" || d.Reason != "mapping must name a local function" {
		t.Fatalf("unexpected diagnostic %v (construct %s)", d, d.Construct)
	}
}
