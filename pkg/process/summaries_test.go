package process_test

import (
	"reflect"
	"strings"
	"testing"

	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/process"
	"generative-architecture/pkg/summaries"
)

// getBindings binds the direct get summary's placeholders to model state.
var getBindings = map[string]string{
	"$exists":          "exists",
	"$object":          "object",
	"$resourceVersion": "version",
	"$snapshot":        "snapshot",
	"$snapshotVersion": "snapshotVersion",
}

func getState() map[string]model.Variable {
	return map[string]model.Variable{
		"exists":          {Type: "bool", Initial: "true"},
		"object":          {Type: "string", Initial: `"stored"`},
		"version":         {Type: "int", Initial: "3"},
		"snapshot":        {Type: "string", Initial: `""`},
		"snapshotVersion": {Type: "int", Initial: "0"},
	}
}

func TestVariantsForInstantiatesEveryOutcome(t *testing.T) {
	s, err := summaries.Builtin().Lookup("get", "direct")
	if err != nil {
		t.Fatal(err)
	}
	variants, err := process.VariantsFor(s, getBindings)
	if err != nil {
		t.Fatal(err)
	}
	want := []process.Variant{
		{Name: "found", Guard: "exists", Updates: map[string]expr.Expression{"snapshot": "object", "snapshotVersion": "version"}},
		{Name: "notFound", Guard: "!exists"},
		{Name: "error", Guard: "true"},
	}
	if !reflect.DeepEqual(variants, want) {
		t.Fatalf("unexpected variants:\n%+v\n%+v", variants, want)
	}
	// A placeholder without a binding is an error, not a silent name.
	if _, err := process.VariantsFor(s, map[string]string{"$exists": "exists"}); err == nil || !strings.Contains(err.Error(), "unbound placeholder") {
		t.Fatalf("unbound placeholder accepted: %v", err)
	}
}

func TestExpandUsesSummaryWhenNoOperationIsWritten(t *testing.T) {
	p := goflow.Program{Entry: 0, Nodes: []goflow.Node{
		{ID: 0, Kind: "get", Consistency: "direct", Bindings: getBindings, Next: 1},
		{ID: 1, Kind: "return"},
	}}
	spec, err := process.Expand(model.Spec{Name: "Get", State: getState()}, p, process.Config{PC: "pc", Finished: "done", Summaries: summaries.Builtin()})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range spec.Actions {
		names = append(names, a.Name)
	}
	if !reflect.DeepEqual(names, []string{"node0_found", "node0_notFound", "node0_error", "node1_return"}) {
		t.Fatalf("unexpected actions %v", names)
	}
	if g := spec.Actions[1].Guard; g != "!done && pc == 0 && (!exists)" {
		t.Fatalf("outcome guard not instantiated: %s", g)
	}
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Next(state, "node0_notFound"); err == nil {
		t.Fatal("notFound enabled while the object exists")
	}
	found, err := m.Next(state, "node0_found")
	if err != nil {
		t.Fatal(err)
	}
	if found["snapshot"] != "stored" || found["snapshotVersion"] != int64(3) || found["pc"] != int64(1) {
		t.Fatalf("found outcome did not record the object: %v", found)
	}
	if _, err := m.Next(state, "node0_error"); err != nil {
		t.Fatalf("transient error outcome disabled: %v", err)
	}

	// A hand-written operation wins over the summary of the same kind.
	custom := process.Config{PC: "pc", Finished: "done", Summaries: summaries.Builtin(), Operations: map[string][]process.Variant{"get": {{Name: "custom", Guard: "true"}}}}
	spec, err = process.Expand(model.Spec{Name: "Get", State: getState()}, p, custom)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Actions) != 2 || spec.Actions[0].Name != "node0_custom" {
		t.Fatalf("hand-written operation not preferred: %+v", spec.Actions)
	}

	// Without a consistency or a binding there is no summary to apply.
	for name, n := range map[string]goflow.Node{
		"consistency": {ID: 0, Kind: "get", Bindings: getBindings, Next: 1},
		"bindings":    {ID: 0, Kind: "get", Consistency: "direct", Next: 1},
	} {
		p.Nodes[0] = n
		if _, err := process.Expand(model.Spec{Name: "Get", State: getState()}, p, process.Config{PC: "pc", Finished: "done", Summaries: summaries.Builtin()}); err == nil {
			t.Fatalf("missing %s accepted", name)
		}
	}
}

func TestExpandDeclaredExternalEffect(t *testing.T) {
	provision := ir.Effect{ID: "provision", Kind: ir.KindExternal, Actor: "controller", Evidence: ir.Declared,
		Target: &ir.Callee{Package: "example.com/provisioner", Func: "Provision"},
		Outcomes: []ir.Outcome{
			{Name: "provisioned", Guard: "!provisioned", Updates: map[string]string{"provisioned": "true"}},
			{Name: "failed", Error: true},
		}}
	p := goflow.Program{Entry: 0, Nodes: []goflow.Node{
		{ID: 0, Kind: goflow.KindEffect, Effect: "provision", Next: 1},
		{ID: 1, Kind: "return"},
	}}
	base := func() model.Spec {
		return model.Spec{Name: "Provision", State: map[string]model.Variable{"provisioned": {Type: "bool", Initial: "false"}}}
	}
	spec, err := process.Expand(base(), p, process.Config{PC: "pc", Finished: "done", External: map[string]ir.Effect{"provision": provision}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range spec.Actions {
		names = append(names, a.Name)
	}
	if !reflect.DeepEqual(names, []string{"node0_provisioned", "node0_failed", "node1_return"}) {
		t.Fatalf("declared outcomes not expanded in order: %v", names)
	}
	// An outcome without a guard is always enabled.
	if g := spec.Actions[1].Guard; g != "!done && pc == 0 && (true)" {
		t.Fatalf("unguarded outcome not always enabled: %s", g)
	}
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	if next, err := m.Next(state, "node0_provisioned"); err != nil || next["provisioned"] != true {
		t.Fatalf("provisioned outcome did not apply its updates: %v %v", next, err)
	}

	// Hand-written semantics keyed by the effect identifier still win, and
	// an effect no document declares fails closed.
	custom := process.Config{PC: "pc", Finished: "done", External: map[string]ir.Effect{"provision": provision}, Operations: map[string][]process.Variant{"provision": {{Name: "custom", Guard: "true"}}}}
	if spec, err = process.Expand(base(), p, custom); err != nil || len(spec.Actions) != 2 || spec.Actions[0].Name != "node0_custom" {
		t.Fatalf("hand-written effect semantics not preferred: %+v %v", spec.Actions, err)
	}
	if _, err := process.Expand(base(), p, process.Config{PC: "pc", Finished: "done"}); err == nil || !strings.Contains(err.Error(), "provision") {
		t.Fatalf("undeclared external effect accepted: %v", err)
	}
}
