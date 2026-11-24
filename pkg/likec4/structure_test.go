package likec4

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/sourcegen"
)

func TestStructureView(t *testing.T) {
	d := sourcegen.Descriptor{
		Resources: []sourcegen.Resource{
			{ID: "database", Group: "demo", Version: "v1", Kind: "Database", Source: sourcegen.Location{File: "api/types.go", Line: 10}},
			{ID: "instance", Group: "demo", Version: "v1", Kind: "DatabaseInstance", Source: sourcegen.Location{File: "api/types.go", Line: 30}},
		},
		Controllers: []sourcegen.Controller{{
			Component: "database", GoType: "demo.Reconciler", Primary: "database", Owns: "instance",
			Source: sourcegen.Location{File: "controller.go", Line: 5},
			Ports: []sourcegen.Port{
				{Name: "endpoint", Direction: "out", Resource: "database", JSONPath: "status.endpoint", Type: "string", Protocol: "status/v1", Evidence: "declared"},
				{Name: "name", Direction: "in", Resource: "instance", JSONPath: "spec.databaseName", Type: "string", Evidence: "declared"},
			},
		}},
	}
	elements, relations, view, err := StructureView("code_structure", d)
	if err != nil {
		t.Fatal(err)
	}
	var w Workspace
	if err := w.Add(elements, relations, view); err != nil {
		t.Fatal(err)
	}
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(elements) != 3 || len(relations) != 2 || len(view.Include) != 3 {
		t.Fatalf("elements %d relations %d include %d", len(elements), len(relations), len(view.Include))
	}
	if relations[0].From != "controller_database" || relations[0].To != "resource_database" {
		t.Errorf("out port drawn %s -> %s", relations[0].From, relations[0].To)
	}
	if relations[1].From != "resource_instance" || relations[1].To != "controller_database" {
		t.Errorf("in port drawn %s -> %s", relations[1].From, relations[1].To)
	}
	src, err := w.Source()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"resource_database = resource 'Database'", "controller_database = controller 'database controller'", "view code_structure", "autoLayout TopBottom"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("source lacks %q:\n%s", want, src)
		}
	}
	d.Controllers[0].Ports[0].Resource = "missing"
	if _, _, _, err := StructureView("code_structure", d); err == nil || !strings.Contains(err.Error(), "unknown resource missing") {
		t.Fatalf("unknown resource accepted: %v", err)
	}
}
