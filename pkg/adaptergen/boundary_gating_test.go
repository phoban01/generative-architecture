package adaptergen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/adaptergen/internal/fixture"
	"github.com/phoban01/generative-architecture/pkg/ir"
)

func boundaryEvents() []adaptergen.BoundaryEvent {
	return []adaptergen.BoundaryEvent{
		{Name: "readDatabase", Effect: "readDatabase"},
		{Name: "readDatabaseFailed", Effect: "readDatabase", Error: true},
		{Name: "readInstance", Effect: "readInstance"},
		{Name: "createInstance", Effect: "createInstance"},
	}
}

func TestGenerateBoundaryGating(t *testing.T) {
	targets := map[string]ir.ObjectRef{"readDatabase": {GVK: fixture.Database, Namespace: "demo", Name: "db-1"}}
	src, err := adaptergen.GenerateBoundaryGating(fixture.Package, fixture.Process(), boundaryEvents(), targets)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "boundary_gating", src)
	p := typecheck(t, src)
	params, results := signature(t, p, "ApplyBoundary")
	wantParams := []string{"context.Context", "*github.com/phoban01/generative-architecture/pkg/effect.Gate", "string"}
	if !reflect.DeepEqual(params, wantParams) {
		t.Errorf("ApplyBoundary takes %v, want %v", params, wantParams)
	}
	if !reflect.DeepEqual(results, []string{"github.com/phoban01/generative-architecture/pkg/adaptergen/internal/typecheck.Boundary", "bool", "error"}) {
		t.Errorf("ApplyBoundary returns %v", results)
	}
	assertNoModelState(t, p)
	text := string(src)
	for _, want := range []string{
		// Steps follow the process from its entry, Next before Else.
		`gatingComplete(ctx, gate, "readDatabase", 1, &effect.Key{Namespace: "demo", Name: "db-1"}, false)`,
		`gatingComplete(ctx, gate, "readDatabase", 1, &effect.Key{Namespace: "demo", Name: "db-1"}, true)`,
		`gatingComplete(ctx, gate, "readInstance", 2, nil, false)`,
		`gatingComplete(ctx, gate, "createInstance", 3, nil, false)`,
		`const BoundarySteps = 3`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated source lacks %q", want)
		}
	}
}

func TestGenerateBoundaryGatingRejects(t *testing.T) {
	db := ir.ObjectRef{GVK: fixture.Database, Namespace: "demo", Name: "db-1"}
	type mutation func(*ir.Process, *[]adaptergen.BoundaryEvent, map[string]ir.ObjectRef)
	for name, c := range map[string]struct {
		mutate mutation
		want   string
	}{
		"no events": {func(_ *ir.Process, e *[]adaptergen.BoundaryEvent, _ map[string]ir.ObjectRef) { *e = nil }, "at least one"},
		"unnamed":   {func(_ *ir.Process, e *[]adaptergen.BoundaryEvent, _ map[string]ir.ObjectRef) { (*e)[0].Name = "" }, "no name"},
		"duplicate name": {func(_ *ir.Process, e *[]adaptergen.BoundaryEvent, _ map[string]ir.ObjectRef) {
			(*e)[1].Name = "readDatabase"
		}, "declared twice"},
		"unknown effect": {func(_ *ir.Process, e *[]adaptergen.BoundaryEvent, _ map[string]ir.ObjectRef) {
			*e = append(*e, adaptergen.BoundaryEvent{Name: "x", Effect: "deleteDatabase"})
		}, "not an effect"},
		"duplicate outcome": {func(_ *ir.Process, e *[]adaptergen.BoundaryEvent, _ map[string]ir.ObjectRef) {
			*e = append(*e, adaptergen.BoundaryEvent{Name: "again", Effect: "readInstance"})
		}, "two boundary events"},
		"no success event": {func(_ *ir.Process, e *[]adaptergen.BoundaryEvent, _ map[string]ir.ObjectRef) { *e = (*e)[:3] }, "no boundary event completes createInstance"},
		"unknown target": {func(_ *ir.Process, _ *[]adaptergen.BoundaryEvent, tg map[string]ir.ObjectRef) {
			tg["deleteDatabase"] = db
		}, "not an effect"},
		"repeated effect": {func(p *ir.Process, _ *[]adaptergen.BoundaryEvent, _ map[string]ir.ObjectRef) {
			p.Nodes[6].Effect = "readInstance"
		}, "performed by nodes"},
		"unreachable effect": {func(p *ir.Process, _ *[]adaptergen.BoundaryEvent, _ map[string]ir.ObjectRef) {
			p.Nodes[3].Else = "n6"
		}, "unreachable"},
		"invalid process": {func(p *ir.Process, _ *[]adaptergen.BoundaryEvent, _ map[string]ir.ObjectRef) { p.Nodes[0].Effect = "" }, "names no effect"},
	} {
		p := fixture.Process()
		p.Nodes = append([]ir.Node(nil), p.Nodes...)
		events := boundaryEvents()
		targets := map[string]ir.ObjectRef{}
		c.mutate(&p, &events, targets)
		_, err := adaptergen.GenerateBoundaryGating(fixture.Package, p, events, targets)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, c.want)
		}
	}
}
