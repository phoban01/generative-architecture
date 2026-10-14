package ir

import (
	"path/filepath"
	"reflect"
	"testing"
)

func sampleDocument() Document {
	prov := Provenance{Generator: "test", Source: Source{File: "f.go", Line: 1, SHA256: "s"}}
	e := validEffect()
	return Document{
		Schema:      SchemaVersion,
		Controllers: []Controller{{ID: "database", Name: "DatabaseReconciler", Package: "example.org/p", Receiver: "DatabaseReconciler", Setup: &Source{File: "wiring.go", Line: 20}, Provenance: prov}},
		Watches: []Watch{{ID: "w1", Controller: "database", Resource: GVK{Group: "demo", Version: "v1alpha1", Kind: "Database"}, Primary: true, Mapping: KeyMapping{Kind: MappingSelf}, Evidence: Extracted, Provenance: prov},
			{ID: "w2", Controller: "database", Resource: GVK{Group: "demo", Version: "v1alpha1", Kind: "DatabaseInstance"}, Predicates: []Predicate{{Kind: "generationChanged"}}, Mapping: KeyMapping{Kind: MappingField, FieldPath: []string{"Spec", "DatabaseName"}}, Evidence: Extracted, Provenance: prov}},
		Effects:     []Effect{e},
		Processes:   []Process{{ID: "p", Controller: "database", Method: "Reconcile", Entry: "n0", Nodes: []Node{{ID: "n0", Kind: NodeEffect, Effect: e.ID, Next: "n1", Source: Source{File: "c.go", Line: 3}}, {ID: "n1", Kind: NodeReturn}}, Provenance: prov}},
		Contracts:   []ContractImplementation{{Contract: "endpoint", Version: "v1", Type: "example.org/p.DatabaseStatus", JSONPaths: map[string]string{"endpoint": ".status.endpoint"}, Evidence: Declared, Provenance: prov}},
		Constraints: []Constraint{{ID: "c1", Name: "publishedRequiresInstance", Expression: "x == y", Scope: "database"}},
	}
}

func TestDocumentRoundTrip(t *testing.T) {
	d := sampleDocument()
	path := filepath.Join(t.TempDir(), "ir.yaml")
	if err := d.Write(path, "sample"); err != nil {
		t.Fatal(err)
	}
	back, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d, back) {
		t.Fatalf("round trip changed the document:\n%+v\n%+v", d, back)
	}
}

func TestDocumentValidate(t *testing.T) {
	d := sampleDocument()
	d.Watches = append(d.Watches, d.Watches[0])
	if err := d.Validate(); err == nil {
		t.Fatal("duplicate watch id accepted")
	}
	d = sampleDocument()
	d.Contracts = append(d.Contracts, d.Contracts[0])
	if err := d.Validate(); err == nil {
		t.Fatal("duplicate contract implementation accepted")
	}
	d = sampleDocument()
	d.Schema = "v0"
	if err := d.Validate(); err == nil {
		t.Fatal("wrong schema accepted")
	}
	d = sampleDocument()
	d.Effects[0].Operation = "watch"
	if err := d.Write(filepath.Join(t.TempDir(), "ir.yaml"), "sample"); err == nil {
		t.Fatal("invalid effect written")
	}
}
