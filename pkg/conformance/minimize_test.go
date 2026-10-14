package conformance_test

import (
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/ir"
	"reflect"
	"testing"
)

// violatingTrace hand-builds a valid counter trace whose state at index 4
// (count == 4) is the first to break the bound; the states that follow still
// violate it. The minimal violating prefix therefore has five states.
func violatingTrace(n int) conformance.Trace {
	t := conformance.Trace{Status: "violation", States: []conformance.State{{Event: "init", Values: map[string]any{"count": int64(0)}}}}
	for i := 1; i < n; i++ {
		t.States = append(t.States, conformance.State{Event: "increment", Values: map[string]any{"count": int64(i)}})
	}
	return t
}

func TestMinimizePrefixReturnsShortestViolatingPrefix(t *testing.T) {
	m := counterModel(t)
	for _, n := range []int{5, 6, 7, 12, 33} {
		got, err := conformance.MinimizePrefix(m, violatingTrace(n))
		if err != nil {
			t.Fatalf("%d states: %v", n, err)
		}
		if len(got.States) != 5 || got.Status != "violation" {
			t.Fatalf("%d states: minimal prefix has %d states", n, len(got.States))
		}
		if !reflect.DeepEqual(got.States, violatingTrace(5).States) {
			t.Fatalf("prefix is not a prefix: %v", got.States)
		}
		if m.Check(got.States[4].Values, got.States[4].Event) == nil || m.Check(got.States[3].Values, got.States[3].Event) != nil {
			t.Fatal("prefix does not end at the first violating state")
		}
	}
	// The result is a copy: editing it leaves the input intact.
	full := violatingTrace(8)
	got, err := conformance.MinimizePrefix(m, full)
	if err != nil {
		t.Fatal(err)
	}
	got.States[0].Event = "changed"
	if full.States[0].Event != "init" || len(full.States) != 8 {
		t.Fatal("input trace aliased")
	}
}

func TestMinimizePrefixEdges(t *testing.T) {
	m := counterModel(t)
	// Violation in the initial state: the prefix is a single state.
	initial := conformance.Trace{Status: "violation", States: []conformance.State{{Event: "init", Values: map[string]any{"count": int64(9)}}, {Event: "increment", Values: map[string]any{"count": int64(10)}}}}
	got, err := conformance.MinimizePrefix(m, initial)
	if err != nil || len(got.States) != 1 {
		t.Fatalf("%d states, %v", len(got.States), err)
	}
	// Violation only in the last state: the whole trace is already minimal.
	got, err = conformance.MinimizePrefix(m, violatingTrace(5))
	if err != nil || len(got.States) != 5 {
		t.Fatalf("%d states, %v", len(got.States), err)
	}
	// No violation: a tool failure, not a shorter trace.
	ok, err := conformance.Scenario(m, "increment", "increment")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conformance.MinimizePrefix(m, ok); err == nil {
		t.Fatal("minimized a satisfying trace")
	} else if f, isFailure := conformance.AsFailure(err); !isFailure || f.Category != conformance.CategoryTool || f.Step != 2 {
		t.Fatal(err)
	}
	if _, err = conformance.MinimizePrefix(m, conformance.Trace{}); conformance.CategoryOf(err) != conformance.CategoryTool {
		t.Fatal(err)
	}
	// A failing check of any kind counts as a violation, as in Replay.
	broken := conformance.Trace{Status: "ok", States: []conformance.State{{Event: "init", Values: map[string]any{"count": int64(0)}}, {Event: "increment", Values: map[string]any{}}}}
	if got, err = conformance.MinimizePrefix(m, broken); err != nil || len(got.States) != 2 {
		t.Fatalf("%d states, %v", len(got.States), err)
	}
}

func TestSourceLinksMapNodeEventsToNodeSources(t *testing.T) {
	p := ir.Process{Nodes: []ir.Node{
		{ID: "3", Kind: ir.NodeEffect, Effect: "read", Source: ir.Source{File: "controller.go", Line: 10, SHA256: "a"}},
		{ID: "12", Kind: ir.NodeBranch, Source: ir.Source{File: "controller.go", Line: 14, SHA256: "a"}},
	}}
	trace := conformance.Trace{States: []conformance.State{{Event: "init"}, {Event: "enqueue"}, {Event: "node3_read"}, {Event: "node12_else_branch"}, {Event: "node7_write"}, {Event: "node_x"}, {Event: "nodes"}}}
	got := conformance.SourceLinks(trace, p)
	want := []ir.Source{{}, {}, p.Nodes[0].Source, p.Nodes[1].Source, {}, {}, {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := conformance.SourceLinks(conformance.Trace{}, p); len(got) != 0 {
		t.Fatal(got)
	}
	for event, want := range map[string]string{"node3_read": "3", "node12_x_y": "12", "init": "", "node_x": "", "node3": "", "xnode3_a": ""} {
		id, ok := conformance.NodeID(event)
		if id != want || ok != (want != "") {
			t.Errorf("NodeID(%q) = %q, %v", event, id, ok)
		}
	}
}
