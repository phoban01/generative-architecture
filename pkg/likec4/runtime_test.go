package likec4

import (
	"testing"

	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"
	"github.com/phoban01/generative-architecture/pkg/ir"
)

func profile() cr.GroupProfile {
	return cr.GroupProfile{Name: "Demo", MaxTicks: 20, Controllers: []cr.ModelController{
		{Name: "mirror", Workers: 1, RetryTicks: 2, AfterTicks: 2, Keys: []cr.ModelKey{{ID: "source", Namespace: "demo", Name: "source"}}},
		{Name: "cleanup", Workers: 2, RetryTicks: 3, AfterTicks: 5, Keys: []cr.ModelKey{{ID: "a", Namespace: "demo", Name: "a"}, {ID: "b", Name: "cluster-wide"}}},
	}}
}

func TestRuntimeViewGolden(t *testing.T) {
	elements, relations, view, err := RuntimeView("runtime", profile(), ir.Source{File: "project.yaml", Line: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 7 || len(relations) != 5 {
		t.Fatalf("%d elements, %d relations", len(elements), len(relations))
	}
	var w Workspace
	if err := w.Add(elements, relations, view); err != nil {
		t.Fatal(err)
	}
	golden(t, "runtime", w)
	likec4Validate(t, w)
}

func TestRuntimeViewRejects(t *testing.T) {
	p := profile()
	p.Controllers[1].Name = "mirror"
	if _, _, _, err := RuntimeView("r", p, ir.Source{}); err == nil {
		t.Error("repeated controller accepted")
	}
	p = profile()
	p.Controllers[0].Workers = 0
	if _, _, _, err := RuntimeView("r", p, ir.Source{}); err == nil {
		t.Error("controller without workers accepted")
	}
}
