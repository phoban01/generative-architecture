package abstraction

import (
	"strings"
	"testing"

	"generative-architecture/pkg/ir"
)

func TestLoadAndDerive(t *testing.T) {
	a, err := Load("testdata/database.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "database" || len(a.Spec.Resources) != 2 || a.Spec.Bounds.MaxInvocations != 4 || len(a.Spec.Excludes) != 2 {
		t.Fatalf("unexpected document %+v", a)
	}
	vars := a.StateVariables()
	want := map[string][2]string{
		"Database_db1_exists":                      {"bool", "false"},
		"Database_db1_status_endpoint":             {"string", `""`},
		"DatabaseInstance_inst1_exists":            {"bool", "false"},
		"DatabaseInstance_inst1_spec_databaseName": {"string", `""`},
		"DatabaseInstance_inst1_status_endpoint":   {"string", `""`},
		"invocations":                              {"int", "0"},
		"compatible":                               {"bool", "false"},
	}
	if len(vars) != len(want) {
		t.Fatalf("expected %d variables, got %v", len(want), a.SortedVariables())
	}
	for name, w := range want {
		v, ok := vars[name]
		if !ok || v.Type != w[0] || string(v.Initial) != w[1] {
			t.Fatalf("%s: got %+v, want %v", name, v, w)
		}
	}
	sorted := a.SortedVariables()
	if sorted[0] != "DatabaseInstance_inst1_exists" || sorted[len(sorted)-1] != "invocations" {
		t.Fatalf("unexpected order %v", sorted)
	}
}

func valid() Abstraction {
	db := ir.GVK{Group: "g", Version: "v1", Kind: "Database"}
	return Abstraction{Name: "x", Spec: Spec{
		Resources: []Resource{{
			GVK:          db,
			Objects:      []Object{{ID: "db1", Namespace: "ns", Name: "db-1"}},
			Fields:       []Field{{Path: []string{"status", "endpoint"}, Type: TypeString}},
			ValueDomains: map[string][]string{"status.endpoint": {`""`, `"a"`}},
		}},
		Locals: []Local{{Name: "n", Type: TypeInt}},
		Bounds: Bounds{MaxObjectsPerResource: 1},
	}}
}

func TestValidateFailures(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		mutate func(*Abstraction)
		want   string
	}{
		"unknown field type":          {func(a *Abstraction) { a.Spec.Resources[0].Fields[0].Type = "float" }, "unknown type"},
		"empty domain":                {func(a *Abstraction) { a.Spec.Resources[0].ValueDomains["status.endpoint"] = nil }, "empty"},
		"domain for undeclared field": {func(a *Abstraction) { a.Spec.Resources[0].ValueDomains["spec.size"] = []string{`"s"`} }, "undeclared field"},
		"no objects":                  {func(a *Abstraction) { a.Spec.Resources[0].Objects = nil }, "at least one object"},
		"too many objects": {func(a *Abstraction) {
			a.Spec.Resources[0].Objects = append(a.Spec.Resources[0].Objects, Object{ID: "db2", Name: "db-2"})
		}, "exceed"},
		"bad object id": {func(a *Abstraction) { a.Spec.Resources[0].Objects[0].ID = "db-1" }, "identifier"},
		"unknown reference target": {func(a *Abstraction) {
			a.Spec.Resources[0].References = []Reference{{Path: []string{"spec", "tenant"}, TargetGVK: ir.GVK{Group: "g", Version: "v1", Kind: "Tenant"}}}
		}, "undeclared resource"},
		"unknown local type": {func(a *Abstraction) { a.Spec.Locals[0].Type = "list" }, "unknown type"},
		"local collides":     {func(a *Abstraction) { a.Spec.Locals[0].Name = "Database_db1_exists" }, "collides"},
		"negative bound":     {func(a *Abstraction) { a.Spec.Bounds.MaxInvocations = -1 }, "negative"},
	}
	for name, c := range cases {
		a := valid()
		c.mutate(&a)
		err := a.Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: got %v, want error containing %q", name, err, c.want)
		}
	}
}

func TestLoadRejectsWrongKind(t *testing.T) {
	if _, err := Load("../../examples/database/model.yaml"); err == nil {
		t.Fatal("TransitionModel accepted as Abstraction")
	}
}
