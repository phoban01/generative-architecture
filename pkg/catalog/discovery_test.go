package catalog

import (
	"testing"

	"generative-architecture/pkg/ir"
)

func TestLoadDiscovery(t *testing.T) {
	entries, err := LoadDiscovery("testdata/discovery.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected three entries (subresource skipped), got %+v", entries)
	}
	c := New()
	for _, e := range entries {
		if err := c.Add(e); err != nil {
			t.Fatal(err)
		}
	}
	cm, ok := c.Lookup(ir.GVK{Version: "v1", Kind: "ConfigMap"})
	if !ok || cm.GVR.Resource != "configmaps" || cm.Scope != ir.Namespaced || cm.Origin != "discovery:testdata/discovery.json" || !cm.Served {
		t.Fatalf("ConfigMap: %+v %v", cm, ok)
	}
	ns, ok := c.Lookup(ir.GVK{Version: "v1", Kind: "Namespace"})
	if !ok || ns.Scope != ir.Cluster {
		t.Fatalf("Namespace: %+v %v", ns, ok)
	}
	if _, err := LoadDiscovery("testdata/missing.json"); err == nil {
		t.Fatal("missing file accepted")
	}
}
