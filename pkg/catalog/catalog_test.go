package catalog

import (
	"testing"

	"generative-architecture/pkg/ir"

	"k8s.io/apimachinery/pkg/api/meta"
)

func entry(kind, plural string, scope ir.Scope) Entry {
	return Entry{
		GVK:    ir.GVK{Group: "refsystem.example.org", Version: "v1alpha1", Kind: kind},
		GVR:    ir.GVR{Group: "refsystem.example.org", Version: "v1alpha1", Resource: plural},
		Scope:  scope,
		Served: true, Storage: true, Origin: "test",
	}
}

func TestAddDetectsConflicts(t *testing.T) {
	c := New()
	if err := c.Add(entry("Tenant", "tenants", ir.Cluster)); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(entry("Tenant", "tenants", ir.Cluster)); err != nil {
		t.Fatalf("identical re-add rejected: %v", err)
	}
	if err := c.Add(entry("Tenant", "tenancies", ir.Cluster)); err == nil {
		t.Fatal("same GVK with a different GVR accepted")
	}
	if err := c.Add(entry("Tenant", "tenants", ir.Namespaced)); err == nil {
		t.Fatal("same GVK with a different scope accepted")
	}
	if err := c.Add(entry("Tenancy", "tenants", ir.Cluster)); err == nil {
		t.Fatal("same GVR with a different GVK accepted")
	}
	bad := entry("Database", "databases", ir.Namespaced)
	bad.GVR.Version = "v1"
	if err := c.Add(bad); err == nil {
		t.Fatal("GVR in a different version accepted")
	}
	if err := c.Add(Entry{GVK: ir.GVK{Version: "v1", Kind: "X"}, GVR: ir.GVR{Version: "v1", Resource: "xs"}}); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if len(c.Entries()) != 1 {
		t.Fatalf("conflicting adds changed the catalog: %+v", c.Entries())
	}
}

func TestLookupAndOrder(t *testing.T) {
	c := New()
	for _, e := range []Entry{entry("Tenant", "tenants", ir.Cluster), entry("Backup", "backups", ir.Namespaced), entry("Database", "databases", ir.Namespaced)} {
		if err := c.Add(e); err != nil {
			t.Fatal(err)
		}
	}
	if e, ok := c.Lookup(ir.GVK{Group: "refsystem.example.org", Version: "v1alpha1", Kind: "Backup"}); !ok || e.GVR.Resource != "backups" {
		t.Fatalf("lookup: %+v %v", e, ok)
	}
	if e, ok := c.ByGVR(ir.GVR{Group: "refsystem.example.org", Version: "v1alpha1", Resource: "databases"}); !ok || e.GVK.Kind != "Database" {
		t.Fatalf("byGVR: %+v %v", e, ok)
	}
	if _, ok := c.ByGVR(ir.GVR{Version: "v1", Resource: "pods"}); ok {
		t.Fatal("unknown GVR resolved")
	}
	got := c.Entries()
	if len(got) != 3 || got[0].GVK.Kind != "Backup" || got[1].GVK.Kind != "Database" || got[2].GVK.Kind != "Tenant" {
		t.Fatalf("entries not sorted: %+v", got)
	}
}

func TestRESTMapper(t *testing.T) {
	c := New()
	if err := c.Add(entry("Tenant", "tenants", ir.Cluster)); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(entry("Database", "databases", ir.Namespaced)); err != nil {
		t.Fatal(err)
	}
	m := c.RESTMapper()
	mapping, err := m.RESTMapping(ir.GVK{Group: "refsystem.example.org", Kind: "Database"}.Schema().GroupKind(), "v1alpha1")
	if err != nil {
		t.Fatal(err)
	}
	if mapping.Resource.Resource != "databases" || mapping.Scope.Name() != meta.RESTScopeNameNamespace {
		t.Fatalf("unexpected mapping %+v", mapping)
	}
	tenant, err := m.RESTMapping(ir.GVK{Group: "refsystem.example.org", Kind: "Tenant"}.Schema().GroupKind(), "v1alpha1")
	if err != nil {
		t.Fatal(err)
	}
	if tenant.Scope.Name() != meta.RESTScopeNameRoot {
		t.Fatal("cluster scope lost")
	}
	gvk, err := m.KindFor(ir.GVR{Group: "refsystem.example.org", Version: "v1alpha1", Resource: "tenants"}.Schema())
	if err != nil || gvk.Kind != "Tenant" {
		t.Fatalf("KindFor: %v %v", gvk, err)
	}
	if _, err := m.RESTMapping(ir.GVK{Group: "apps", Kind: "Deployment"}.Schema().GroupKind(), "v1"); err == nil {
		t.Fatal("uncatalogued kind resolved")
	}
}
