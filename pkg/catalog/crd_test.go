package catalog

import (
	"path/filepath"
	"strings"
	"testing"

	"generative-architecture/pkg/ir"
)

func TestLoadCRDsFromReferenceSystem(t *testing.T) {
	paths, err := filepath.Glob("../../examples/refsystem/config/crd/*.yaml")
	if err != nil || len(paths) != 3 {
		t.Fatalf("expected three CRD files, got %v (%v)", paths, err)
	}
	entries, err := LoadCRDs(paths...)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected three entries, got %+v", entries)
	}
	c := New()
	for _, e := range entries {
		if err := c.Add(e); err != nil {
			t.Fatal(err)
		}
		if !e.Served || !e.Storage || !strings.HasSuffix(e.Origin, ".yaml") {
			t.Fatalf("incomplete entry %+v", e)
		}
	}
	for kind, plural := range map[string]string{"Tenant": "tenants", "Database": "databases", "Backup": "backups"} {
		e, ok := c.Lookup(ir.GVK{Group: "refsystem.example.org", Version: "v1alpha1", Kind: kind})
		if !ok || e.GVR.Resource != plural || e.Scope != ir.Namespaced {
			t.Fatalf("%s: %+v %v", kind, e, ok)
		}
	}
}

func TestDecodeCRDsMultiDocumentAndRejects(t *testing.T) {
	doc := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.org
spec:
  group: example.org
  scope: Cluster
  names:
    plural: widgets
    kind: Widget
  versions:
    - name: v1alpha1
      served: false
      storage: false
    - name: v1
      served: true
      storage: true
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gadgets.example.org
spec:
  group: example.org
  scope: Namespaced
  names:
    plural: gadgets
    kind: Gadget
  versions:
    - name: v1
      served: true
      storage: true
`
	entries, err := decodeCRDs(strings.NewReader(doc), "inline")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].GVK.Version != "v1" || entries[0].Scope != ir.Cluster || entries[1].GVR.Resource != "gadgets" {
		t.Fatalf("unexpected entries %+v", entries)
	}
	if _, err := decodeCRDs(strings.NewReader("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n"), "inline"); err == nil {
		t.Fatal("non-CRD document accepted")
	}
}
