package likec4

import (
	"testing"

	"github.com/phoban01/generative-architecture/pkg/composition"
	"github.com/phoban01/generative-architecture/pkg/ir"
)

func twoComponents() ([]composition.Component, map[string]ir.Evidence, []composition.Link, map[string]ir.Source) {
	components := []composition.Component{
		{Name: "producer", Ports: map[string]composition.Port{"output": {State: "value", Contract: "example.org/value", Version: "v1", Direction: "out"}}},
		{Name: "upstream", Ports: map[string]composition.Port{"input": {State: "seen", Contract: "example.org/value", Version: "v1", Direction: "in"}}},
	}
	evidence := map[string]ir.Evidence{"producer": ir.Extracted, "upstream": ir.Declared}
	links := []composition.Link{{From: "producer.output", To: "upstream.input"}}
	sources := map[string]ir.Source{"producer": {File: "producer/controller.go", Line: 20}}
	return components, evidence, links, sources
}

func TestContextViewGolden(t *testing.T) {
	components, evidence, links, sources := twoComponents()
	elements, relations, view, err := ContextView("pipeline", components, evidence, links, sources)
	if err != nil {
		t.Fatal(err)
	}
	var w Workspace
	if err := w.Add(elements, relations, view); err != nil {
		t.Fatal(err)
	}
	// The extracted component is a controller, the declared one a boundary,
	// and the link is only as strong as the declared end.
	if elements[0].Kind != KindController || elements[1].Kind != KindBoundary || relations[0].Evidence != ir.Declared {
		t.Fatalf("kinds or evidence: %+v %+v", elements, relations)
	}
	golden(t, "context", w)
	likec4Validate(t, w)
}

func TestContextViewRejects(t *testing.T) {
	components, evidence, links, sources := twoComponents()
	if _, _, _, err := ContextView("p", components, map[string]ir.Evidence{"producer": ir.Extracted}, links, sources); err == nil {
		t.Error("component without evidence accepted")
	}
	if _, _, _, err := ContextView("p", components, evidence, []composition.Link{{From: "producer.missing", To: "upstream.input"}}, sources); err == nil {
		t.Error("unknown port accepted")
	}
	components[1].Ports["input"] = composition.Port{Contract: "example.org/value", Version: "v2"}
	if _, _, _, err := ContextView("p", components, evidence, links, sources); err == nil {
		t.Error("version mismatch accepted")
	}
}
