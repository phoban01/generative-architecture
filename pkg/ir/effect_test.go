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

func validExternalEffect() Effect {
	return Effect{
		ID: "provision", Kind: KindExternal, Actor: "database",
		Target: &Callee{Package: "example.local/provisioner", Receiver: "Client", Func: "Provision"},
		Outcomes: []Outcome{
			{Name: "provisioned", Guard: "!instance.ready", Updates: map[string]string{"instance.ready": "true"}},
			{Name: "failed", Error: true},
		},
		Evidence:   Declared,
		Provenance: Provenance{Generator: "summaries", Source: Source{File: "provision.go", Line: 12, SHA256: "b"}},
	}
}

func TestExternalEffectValidate(t *testing.T) {
	if err := validExternalEffect().Validate(); err != nil {
		t.Fatal(err)
	}
	assumed := validExternalEffect()
	assumed.Evidence = Assumed
	if err := assumed.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Effect){
		"extracted evidence":  func(e *Effect) { e.Evidence = Extracted },
		"observed evidence":   func(e *Effect) { e.Evidence = Observed },
		"no target":           func(e *Effect) { e.Target = nil },
		"target without func": func(e *Effect) { e.Target = &Callee{Package: "example.local/provisioner"} },
		"no outcomes":         func(e *Effect) { e.Outcomes = nil },
		"unnamed outcome":     func(e *Effect) { e.Outcomes[0].Name = "" },
		"duplicate outcome":   func(e *Effect) { e.Outcomes[1].Name = e.Outcomes[0].Name },
		"api operation":       func(e *Effect) { e.Operation = OpCreate },
		"api consistency":     func(e *Effect) { e.Consistency = ConsistencyDirect },
		"malformed resource":  func(e *Effect) { e.Resource = GVK{Kind: "Database"} },
		"unknown kind":        func(e *Effect) { e.Kind = "remote" },
	} {
		e := validExternalEffect()
		mutate(&e)
		if err := e.Validate(); err == nil {
			t.Fatalf("invalid external effect accepted: %s", name)
		}
	}
	api := validEffect()
	api.Target = validExternalEffect().Target
	if err := api.Validate(); err == nil {
		t.Fatal("api effect with a target accepted")
	}
	api = validEffect()
	api.Kind = KindAPI
	if err := api.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExternalEffectStableID(t *testing.T) {
	base := validExternalEffect()
	id := base.StableID()
	moved := validExternalEffect()
	moved.ID, moved.Provenance.Source.Line, moved.Evidence = "other", 1, Assumed
	moved.Outcomes = moved.Outcomes[:1]
	if moved.StableID() != id {
		t.Fatal("provenance, evidence or outcomes changed the stable id")
	}
	other := validExternalEffect()
	other.Target = &Callee{Package: "example.local/provisioner", Receiver: "Client", Func: "Deprovision"}
	if other.StableID() == id {
		t.Fatal("target change kept the id")
	}
	if validEffect().StableID() == id {
		t.Fatal("external and api effects share an id")
	}
}

func TestDocumentValidatesExternalEffects(t *testing.T) {
	bad := validExternalEffect()
	bad.Evidence = Extracted
	d := Document{Schema: SchemaVersion, Effects: []Effect{validEffect(), bad}}
	if err := d.Validate(); err == nil {
		t.Fatal("document accepted an external effect claiming extracted evidence")
	}
	d.Effects[1].Evidence = Declared
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}
