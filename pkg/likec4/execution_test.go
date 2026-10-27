package likec4

import (
	"testing"

	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/ir"
)

func shortTrace() conformance.Trace {
	return conformance.Trace{Status: "violation", States: []conformance.State{
		{Event: "init", Values: map[string]any{"pc": int64(-1), "copy": "", "queued": false}},
		{Event: "enqueue", Values: map[string]any{"pc": int64(-1), "copy": "", "queued": true}},
		{Event: "idle", Values: map[string]any{"pc": int64(-1), "copy": "", "queued": true}},
		{Event: "begin", Values: map[string]any{"pc": int64(0), "copy": "", "queued": false}},
		{Event: "node0_update", Values: map[string]any{"pc": int64(1), "copy": "wrong", "queued": false}},
	}}
}

func TestExecutionViewGolden(t *testing.T) {
	links := make([]ir.Source, 5)
	links[4] = ir.Source{File: "controller.go", Line: 42}
	elements, relations, view, err := ExecutionView("wrong_write", shortTrace(), links)
	if err != nil {
		t.Fatal(err)
	}
	// init plus three events; idle is skipped.
	if len(elements) != 4 || len(view.Steps) != 3 || elements[3].Evidence != ir.Extracted || elements[1].Evidence != ir.Assumed {
		t.Fatalf("elements %+v, steps %+v", elements, view.Steps)
	}
	var w Workspace
	if err := w.Add(elements, relations, view); err != nil {
		t.Fatal(err)
	}
	golden(t, "execution", w)
	likec4Validate(t, w)
}

func TestExecutionViewRejects(t *testing.T) {
	if _, _, _, err := ExecutionView("x", shortTrace(), make([]ir.Source, 2)); err == nil {
		t.Error("wrong number of links accepted")
	}
	if _, _, _, err := ExecutionView("x", conformance.Trace{}, nil); err == nil {
		t.Error("empty trace accepted")
	}
	idle := conformance.Trace{States: []conformance.State{{Event: "init"}, {Event: "idle"}}}
	if _, _, _, err := ExecutionView("x", idle, nil); err == nil {
		t.Error("idle-only trace accepted")
	}
}
