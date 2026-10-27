package adaptergen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/adaptergen/internal/fixture"
	"github.com/phoban01/generative-architecture/pkg/ir"
)

func TestGenerateGating(t *testing.T) {
	src, err := adaptergen.GenerateGating(fixture.Package, fixture.Process())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "gating", src)
	p := typecheck(t, src)
	params, results := signature(t, p, "ApplyProcessEvent")
	wantParams := []string{"context.Context", "*github.com/phoban01/generative-architecture/pkg/effect.Gate", "map[string]github.com/phoban01/generative-architecture/pkg/ir.Node", "string", "func(id string, r github.com/phoban01/generative-architecture/pkg/effect.Result)"}
	if !reflect.DeepEqual(params, wantParams) {
		t.Errorf("ApplyProcessEvent takes %v, want %v", params, wantParams)
	}
	if !reflect.DeepEqual(results, []string{"error"}) {
		t.Errorf("ApplyProcessEvent returns %v", results)
	}
	assertNoModelState(t, p)
	text := string(src)
	for _, want := range []string{
		`gatingBoundary(ctx, gate, event, "readDatabase", observe)`,
		`gatingBoundary(ctx, gate, event, "readInstance", observe)`,
		`gatingBoundary(ctx, gate, event, "createInstance", observe)`,
		`return gatingExpect(event, node, ir.NodeBranch, "")`,
		`return gatingExpect(event, node, ir.NodeAssign, "")`,
		`return gatingExpect(event, node, ir.NodeReturn, "")`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated source lacks %q", want)
		}
	}
	if strings.Count(text, "gatingBoundary(ctx, gate, event") != 3 {
		t.Errorf("expected one boundary per effect node:\n%s", text)
	}
}

func TestGenerateGatingRejects(t *testing.T) {
	effectNode := ir.Node{ID: "a", Kind: ir.NodeEffect, Effect: "read"}
	process := func(nodes ...ir.Node) ir.Process {
		return ir.Process{ID: "p", Nodes: nodes}
	}
	cases := map[string]struct {
		pkg  string
		p    ir.Process
		want string
	}{
		"bad package":     {"a b", process(effectNode), "not a Go identifier"},
		"no id":           {"p", ir.Process{Nodes: []ir.Node{effectNode}}, "no id"},
		"no nodes":        {"p", process(), "has no nodes"},
		"node without id": {"p", process(ir.Node{Kind: ir.NodeReturn}), "node has no id"},
		"duplicate node":  {"p", process(effectNode, effectNode), "duplicate node"},
		"bad entry":       {"p", ir.Process{ID: "p", Entry: "x", Nodes: []ir.Node{effectNode}}, "entry x is not a node"},
		"unknown kind":    {"p", process(ir.Node{ID: "a", Kind: "loop"}), "unknown kind"},
		"effect unnamed":  {"p", process(ir.Node{ID: "a", Kind: ir.NodeEffect}), "names no effect"},
		"branch effect":   {"p", process(ir.Node{ID: "a", Kind: ir.NodeBranch, Effect: "read"}), "branch node a names effect"},
		"dangling next":   {"p", process(ir.Node{ID: "a", Kind: ir.NodeBranch, Next: "b"}), "unknown node b"},
		"dangling else":   {"p", process(ir.Node{ID: "a", Kind: ir.NodeBranch, Else: "c"}), "unknown node c"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := adaptergen.GenerateGating(c.pkg, c.p)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want %q", err, c.want)
			}
		})
	}
}
