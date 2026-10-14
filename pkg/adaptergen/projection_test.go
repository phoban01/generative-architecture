package adaptergen_test

import (
	"reflect"
	"strings"
	"testing"

	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/adaptergen/internal/fixture"
	"generative-architecture/pkg/ir"
)

func TestGenerateProjection(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	ps := adaptergen.FromAbstraction(a)
	var states []string
	for _, p := range ps {
		states = append(states, p.State)
	}
	want := []string{"Database_db1_exists", "Database_db1_status_endpoint", "DatabaseInstance_inst1_exists", "DatabaseInstance_inst1_spec_databaseName", "DatabaseInstance_inst1_status_endpoint"}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("projections %v, want %v", states, want)
	}
	src, err := adaptergen.GenerateProjection(fixture.Package, ps)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "projection", src)
	if !strings.HasPrefix(string(src), adaptergen.Header+"\n") {
		t.Fatalf("missing generated-code header:\n%s", src)
	}
	p := typecheck(t, src)
	params, results := signature(t, p, "Project")
	wantParams := []string{"context.Context", "k8s.io/client-go/dynamic.Interface", "k8s.io/apimachinery/pkg/api/meta.RESTMapper"}
	if !reflect.DeepEqual(params, wantParams) {
		t.Errorf("Project takes %v, want only API clients %v", params, wantParams)
	}
	if !reflect.DeepEqual(results, []string{"map[string]any", "error"}) {
		t.Errorf("Project returns %v", results)
	}
	assertNoModelState(t, p)
}

func TestGenerateProjectionDefaultsExistenceFlag(t *testing.T) {
	key := ir.ObjectRef{Name: "only"}
	src, err := adaptergen.GenerateProjection("p", []adaptergen.Projection{
		{State: "ready", Resource: fixture.Database, Key: key, JSONPath: []string{"status", "ready"}, Type: "bool"},
		{State: "count", Resource: fixture.Database, Key: key, JSONPath: []string{"status", "count"}, Type: "int"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, want := range []string{
		`state["readyExists"] = found`,
		`state["countExists"] = found`,
		`state["ready"] = false`,
		`state["count"] = int64(0)`,
		`unstructured.NestedBool(obj.Object, "status", "ready")`,
		`unstructured.NestedInt64(obj.Object, "status", "count")`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated source lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "projectGet(ctx, api, mapper") != 1 {
		t.Errorf("expected one Get for the object:\n%s", text)
	}
	typecheck(t, src)
}

func TestGenerateProjectionRejects(t *testing.T) {
	key := ir.ObjectRef{Namespace: "demo", Name: "db-1"}
	field := adaptergen.Projection{State: "endpoint", Resource: fixture.Database, Key: key, JSONPath: []string{"status", "endpoint"}, Type: "string"}
	cases := map[string]struct {
		pkg  string
		ps   []adaptergen.Projection
		want string
	}{
		"no projections":      {"p", nil, "at least one projection"},
		"bad package":         {"go-gen", []adaptergen.Projection{field}, "not a Go identifier"},
		"empty state":         {"p", []adaptergen.Projection{{Resource: fixture.Database, Key: key, Type: "bool"}}, "no state variable"},
		"duplicate state":     {"p", []adaptergen.Projection{field, field}, "projected twice"},
		"unknown type":        {"p", []adaptergen.Projection{{State: "x", Resource: fixture.Database, Key: key, JSONPath: []string{"a"}, Type: "float"}}, "unknown type"},
		"missing name":        {"p", []adaptergen.Projection{{State: "x", Resource: fixture.Database, Type: "bool"}}, "no name"},
		"missing kind":        {"p", []adaptergen.Projection{{State: "x", Key: key, Type: "bool"}}, "version and kind are required"},
		"foreign key":         {"p", []adaptergen.Projection{{State: "x", Resource: fixture.Database, Key: ir.ObjectRef{GVK: fixture.Instance, Namespace: "demo", Name: "n"}, Type: "bool"}}, "does not belong"},
		"other cluster":       {"p", []adaptergen.Projection{{State: "x", Resource: fixture.Database, Key: ir.ObjectRef{Cluster: "east", Namespace: "demo", Name: "n"}, Type: "bool"}}, "not supported"},
		"string existence":    {"p", []adaptergen.Projection{{State: "x", Resource: fixture.Database, Key: key, Type: "string"}}, "must be bool"},
		"empty path element":  {"p", []adaptergen.Projection{{State: "x", Resource: fixture.Database, Key: key, JSONPath: []string{"status", ""}, Type: "string"}}, "empty element"},
		"existence collision": {"p", []adaptergen.Projection{field, {State: "endpointExists", Resource: fixture.Instance, Key: key, Type: "bool"}}, "collides"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := adaptergen.GenerateProjection(c.pkg, c.ps)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want %q", err, c.want)
			}
		})
	}
}
