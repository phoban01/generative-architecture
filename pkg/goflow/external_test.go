package goflow

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/ir"
)

const (
	externalPackage = "github.com/phoban01/generative-architecture/pkg/goflow/testdata/external"
	provisionCallee = "github.com/phoban01/generative-architecture/pkg/goflow/testdata/external/provisioner.Provision"
)

func externalOptions(t *testing.T) Options {
	t.Helper()
	effects, err := ReadExternalEffects(filepath.Join(moduleRoot(t), "pkg/goflow/testdata/external/effects.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return Options{External: effects, Scope: map[string]expr.Symbol{"id": {Type: "string", Quint: "id"}}, Returns: []Return{{Type: "string"}, {Error: true}}}
}

func TestExternalEffectsResolveByCallee(t *testing.T) {
	opts := externalOptions(t)
	callee := Callee{Package: externalPackage + "/provisioner", Method: "Provision"}
	e, ok := opts.External[callee]
	if !ok || e.ID != "provision" || !e.External() || e.Evidence != ir.Declared || len(e.Outcomes) != 2 {
		t.Fatalf("effect not resolved by callee: %+v", opts.External)
	}
	if e.Outcomes[1].Name != "failed" || !e.Outcomes[1].Error {
		t.Fatalf("declared outcome order or error flag lost: %+v", e.Outcomes)
	}
	if byID := ExternalByID(opts.External); byID["provision"].ID != "provision" {
		t.Fatalf("re-keying by identifier failed: %+v", byID)
	}
	if got := callee.String(); got != provisionCallee {
		t.Fatalf("unexpected callee rendering %s", got)
	}

	// Declarations are validated with the IR rules for external effects.
	target := ir.Callee{Package: "example.com/p", Func: "F"}
	outcome := []ir.Outcome{{Name: "done"}}
	for name, doc := range map[string]ExternalEffects{
		"no id":            {Effects: []ExternalEffect{{Target: target, Outcomes: outcome}}},
		"duplicate id":     {Effects: []ExternalEffect{{ID: "a", Target: target, Outcomes: outcome}, {ID: "a", Target: ir.Callee{Package: "example.com/p", Func: "G"}, Outcomes: outcome}}},
		"duplicate target": {Effects: []ExternalEffect{{ID: "a", Target: target, Outcomes: outcome}, {ID: "b", Target: target, Outcomes: outcome}}},
		"no outcomes":      {Effects: []ExternalEffect{{ID: "a", Target: target}}},
		"no target":        {Effects: []ExternalEffect{{ID: "a", Outcomes: outcome}}},
		"unnamed outcome":  {Effects: []ExternalEffect{{ID: "a", Target: target, Outcomes: []ir.Outcome{{Guard: "true"}}}}},
	} {
		if _, err := doc.Resolve(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestExternalCallLowersToEffectNode(t *testing.T) {
	p, err := ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/external", "Reconciler", "Reconcile"), externalOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	effect := p.Nodes[p.Entry]
	if effect.Kind != KindEffect || effect.Effect != "provision" || effect.Source != "err := provisioner.Provision(id)" {
		t.Fatalf("call not lowered to its declared effect: %+v", effect)
	}
	if !reflect.DeepEqual(effect.Results, []expr.Expression{"err"}) {
		t.Fatalf("error result not bound: %+v", effect)
	}
	branch := p.Nodes[effect.Next]
	if branch.Kind != "branch" || branch.Expression != "err" {
		t.Fatalf("error from the effect not recognised: %+v", branch)
	}
	if p.Nodes[branch.Next].ReturnError != "err" || p.Nodes[branch.Else].Expression != `"provisioned"` {
		t.Fatalf("returns after the effect differ: %+v %+v", p.Nodes[branch.Next], p.Nodes[branch.Else])
	}

	// A call to a symbol nothing declares fails closed, naming the callee.
	_, err = ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/external", "Reconciler", "Helper"), externalOptions(t))
	d, ok := AsDiagnostic(err)
	if !ok || d.Construct != "call" || d.Source != "normalize(id)" || !strings.Contains(d.Reason, externalPackage+".normalize") {
		t.Fatalf("expected a diagnostic naming the callee, got %v", err)
	}
	// Without any declaration the same call is still a located diagnostic.
	_, err = ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/external", "Reconciler", "Reconcile"), Options{Scope: map[string]expr.Symbol{"id": {Type: "string", Quint: "id"}}, Returns: []Return{{Type: "string"}, {Error: true}}})
	if d, ok := AsDiagnostic(err); !ok || d.Line == 0 || !strings.Contains(d.Reason, provisionCallee) {
		t.Fatalf("undeclared external call accepted: %v", err)
	}
}
