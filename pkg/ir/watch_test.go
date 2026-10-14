package ir

import "testing"

func TestWatchStableID(t *testing.T) {
	base := Watch{
		Controller: "database",
		Resource:   GVK{Group: "demo", Version: "v1alpha1", Kind: "DatabaseInstance"},
		Predicates: []Predicate{{Kind: "generationChanged"}, {Kind: "resourceVersionChanged"}},
		Mapping:    KeyMapping{Kind: MappingField, FieldPath: []string{"Spec", "DatabaseName"}},
		Evidence:   Extracted,
		Provenance: Provenance{Generator: "watchgen", Source: Source{File: "wiring.go", Line: 31, SHA256: "a"}},
	}
	id := base.StableID()
	if len(id) != 16 {
		t.Fatalf("unexpected id %q", id)
	}
	reordered := base
	reordered.Predicates = []Predicate{{Kind: "resourceVersionChanged"}, {Kind: "generationChanged"}}
	moved := base
	moved.Provenance.Source.Line = 99
	for name, w := range map[string]Watch{"reordered predicates": reordered, "moved": moved} {
		if w.StableID() != id {
			t.Fatalf("%s changed the id", name)
		}
	}
	changed := base
	changed.Mapping.FieldPath = []string{"Name"}
	if changed.StableID() == id {
		t.Fatal("field path change kept the id")
	}
	other := base
	other.Controller = "backup"
	if other.StableID() == id {
		t.Fatal("controller change kept the id")
	}
}

func TestKeyMappingValidate(t *testing.T) {
	if err := (KeyMapping{Kind: MappingSelf}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (KeyMapping{Kind: MappingField}).Validate(); err == nil {
		t.Fatal("field mapping without path accepted")
	}
	if err := (KeyMapping{Kind: MappingOwner}).Validate(); err == nil {
		t.Fatal("owner mapping without GVK accepted")
	}
	if err := (KeyMapping{Kind: MappingOwner, OwnerGVK: &GVK{Version: "v1", Kind: "Database"}}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (KeyMapping{Kind: "regex"}).Validate(); err == nil {
		t.Fatal("unknown kind accepted")
	}
	w := Watch{ID: "x", Controller: "c", Resource: GVK{Version: "v1", Kind: "K"}, Mapping: KeyMapping{Kind: MappingSelf}}
	if err := w.Validate(); err == nil {
		t.Fatal("missing evidence accepted")
	}
	w.Evidence = Declared
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestStableIDSeparatesParts(t *testing.T) {
	if stableID("ab", "c") == stableID("a", "bc") {
		t.Fatal("parts collide by concatenation")
	}
}
