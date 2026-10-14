package ir

import "testing"

func validEffect() Effect {
	return Effect{
		ID: "e", Actor: "database", Resource: GVK{Group: "demo", Version: "v1alpha1", Kind: "Database"},
		Operation: OpUpdate, Consistency: ConsistencyDirect, Subresource: "status",
		Options: map[string]string{"fieldManager": "database"}, Evidence: Extracted,
		Provenance: Provenance{Generator: "dynamicgen", Source: Source{File: "store.go", Line: 77, SHA256: "a"}},
	}
}

func TestEffectValidate(t *testing.T) {
	if err := validEffect().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Effect){
		"operation":   func(e *Effect) { e.Operation = "watch" },
		"consistency": func(e *Effect) { e.Consistency = "eventual" },
		"evidence":    func(e *Effect) { e.Evidence = "" },
		"resource":    func(e *Effect) { e.Resource.Kind = "" },
		"actor":       func(e *Effect) { e.Actor = "" },
	} {
		e := validEffect()
		mutate(&e)
		if err := e.Validate(); err == nil {
			t.Fatalf("invalid %s accepted", name)
		}
	}
}

func TestEffectStableID(t *testing.T) {
	base := validEffect()
	id := base.StableID()
	moved := validEffect()
	moved.Provenance.Source.Line = 1
	moved.ID = "other"
	if moved.StableID() != id {
		t.Fatal("provenance or ID changed the stable id")
	}
	changed := validEffect()
	changed.Operation = OpPatch
	if changed.StableID() == id {
		t.Fatal("operation change kept the id")
	}
	sub := validEffect()
	sub.Subresource = ""
	if sub.StableID() == id {
		t.Fatal("subresource change kept the id")
	}
}
