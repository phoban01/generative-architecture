package likec4

import (
	"strings"
	"testing"

	"generative-architecture/pkg/ir"
)

func resourceDocument() ir.Document {
	database := ir.GVK{Group: "demo.example", Version: "v1", Kind: "Database"}
	instance := ir.GVK{Group: "demo.example", Version: "v1", Kind: "Instance"}
	prov := func(line int) ir.Provenance {
		return ir.Provenance{Generator: "test", Source: ir.Source{File: "controller.go", Line: line, SHA256: "x"}}
	}
	return ir.Document{
		Controllers: []ir.Controller{{ID: "example.Reconciler", Name: "Reconciler", Setup: &ir.Source{File: "wiring.go", Line: 10}, Provenance: prov(1)}},
		Watches: []ir.Watch{
			{ID: "w1", Controller: "example.Reconciler", Resource: database, Primary: true, Mapping: ir.KeyMapping{Kind: ir.MappingSelf}, Evidence: ir.Extracted, Provenance: ir.Provenance{Generator: "test", Source: ir.Source{File: "wiring.go", Line: 12}}},
			{ID: "w2", Controller: "example.Reconciler", Resource: instance, Mapping: ir.KeyMapping{Kind: ir.MappingField, FieldPath: []string{"spec", "databaseName"}}, Predicates: []ir.Predicate{{Kind: "resourceVersionChanged"}, {Kind: "label", Args: map[string]string{"app": "db"}}}, Evidence: ir.Extracted, Provenance: ir.Provenance{Generator: "test", Source: ir.Source{File: "wiring.go", Line: 13}}},
		},
		Effects: []ir.Effect{
			{ID: "readDatabase", Actor: "example.Reconciler", Resource: database, Operation: ir.OpGet, Consistency: ir.ConsistencyCached, Evidence: ir.Extracted, Provenance: prov(20)},
			{ID: "createInstance", Actor: "example.Reconciler", Resource: instance, Operation: ir.OpCreate, Consistency: ir.ConsistencyDirect, Evidence: ir.Extracted, Provenance: prov(30)},
			{ID: "publish", Actor: "example.Reconciler", Resource: database, Operation: ir.OpUpdate, Subresource: "status", Consistency: ir.ConsistencyDirect, Evidence: ir.Declared, Provenance: prov(40)},
		},
	}
}

func TestResourceViewGolden(t *testing.T) {
	elements, relations, view, err := ResourceView("database", resourceDocument())
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 3 || len(relations) != 5 {
		t.Fatalf("%d elements, %d relations", len(elements), len(relations))
	}
	var w Workspace
	if err := w.Add(elements, relations, view); err != nil {
		t.Fatal(err)
	}
	golden(t, "resources", w)
	likec4Validate(t, w)
}

func TestResourceViewExternalAndUndeclared(t *testing.T) {
	d := resourceDocument()
	d.Effects = append(d.Effects, ir.Effect{ID: "notify", Kind: ir.KindExternal, Actor: "example.Reconciler", Target: &ir.Callee{Package: "example.org/mail", Func: "Send"}, Outcomes: []ir.Outcome{{Name: "sent"}, {Name: "failed", Error: true}}, Evidence: ir.Declared})
	elements, relations, _, err := ResourceView("database", d)
	if err != nil {
		t.Fatal(err)
	}
	last := relations[len(relations)-1]
	if last.Label != "calls" || !strings.Contains(last.Description, "outcomes sent, failed") || elements[1].Kind != KindBoundary {
		t.Fatalf("external effect drawn as %+v; elements %+v", last, elements)
	}
	d.Watches[0].Controller = "other"
	if _, _, _, err := ResourceView("database", d); err == nil {
		t.Fatal("watch of an undeclared controller accepted")
	}
}
