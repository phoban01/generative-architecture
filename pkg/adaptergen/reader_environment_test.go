package adaptergen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/adaptergen/internal/fixture"
)

func TestGenerateReaderEnvironment(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	src, err := adaptergen.GenerateReaderEnvironment(fixture.Package, a.Spec, []string{"db1"}, []string{"db1", "inst1"})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "reader_environment", src)
	p := typecheck(t, src)
	params, results := signature(t, p, "ApplyEnvironment")
	wantParams := []string{"context.Context", "sigs.k8s.io/controller-runtime/pkg/client.Client", "sigs.k8s.io/controller-runtime/pkg/client.Client", "string"}
	if !reflect.DeepEqual(params, wantParams) {
		t.Errorf("ApplyEnvironment takes %v, want %v", params, wantParams)
	}
	if !reflect.DeepEqual(results, []string{"bool", "error"}) {
		t.Errorf("ApplyEnvironment returns %v", results)
	}
	assertNoModelState(t, p)
	text := string(src)
	for _, want := range []string{
		`case "changeDb1":`,
		// The zero value is left out of the cycle.
		`environmentCycle(obj, []any{"db-1.internal"}, "status", "endpoint")`,
		`case "deliverCache":`,
		`environmentCopy(live, cached, "spec", "databaseName")`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated source lacks %q", want)
		}
	}

	// Without cached objects there is no cache client and no delivery.
	src, err = adaptergen.GenerateReaderEnvironment(fixture.Package, a.Spec, []string{"db1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if params, _ := signature(t, typecheck(t, src), "ApplyEnvironment"); len(params) != 3 || strings.Contains(string(src), "deliverCache") {
		t.Fatalf("change-only environment takes %v", params)
	}
}

func TestGenerateReaderEnvironmentRejects(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		changed, cached []string
		want            string
	}{
		"nothing":            {nil, nil, "at least one"},
		"unknown object":     {[]string{"db9"}, nil, "not an object"},
		"no domain":          {[]string{"inst1"}, nil, "no declared field with a value domain"},
		"changed twice":      {[]string{"db1", "db1"}, nil, "twice"},
		"unknown cached":     {nil, []string{"db9"}, "not an object"},
		"cached twice":       {nil, []string{"db1", "db1"}, "twice"},
		"bad package":        {[]string{"db1"}, nil, "identifier"},
		"zero-only domain":   {[]string{"db1"}, nil, "no value to change to"},
		"non-literal domain": {[]string{"db1"}, nil, "not a string literal"},
	} {
		spec := a.Spec
		spec.Resources = append([]abstraction.Resource(nil), a.Spec.Resources...)
		pkg := fixture.Package
		switch name {
		case "bad package":
			pkg = "not-a-package"
		case "zero-only domain":
			spec.Resources[0].ValueDomains = map[string][]string{"status.endpoint": {`""`}}
		case "non-literal domain":
			spec.Resources[0].ValueDomains = map[string][]string{"status.endpoint": {`endpoint`}}
		}
		_, err := adaptergen.GenerateReaderEnvironment(pkg, spec, c.changed, c.cached)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, c.want)
		}
	}
	if got := adaptergen.ChangeEvent("source"); got != "changeSource" {
		t.Fatalf("ChangeEvent(source) = %s", got)
	}
}

func TestGenerateReaderValueEnvironment(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	src, err := adaptergen.GenerateReaderValueEnvironment(fixture.Package, a.Spec, []string{"db1"}, []string{"db1"})
	if err != nil {
		t.Fatal(err)
	}
	p := typecheck(t, src)
	params, _ := signature(t, p, "ApplyEnvironment")
	if len(params) != 4 {
		t.Fatalf("ApplyEnvironment takes %v", params)
	}
	assertNoModelState(t, p)
	text := string(src)
	// One event per domain value, the zero value included; no cycling.
	for _, want := range []string{
		`case "changeDb1_empty":`,
		`unstructured.SetNestedField(obj.Object, "", "status", "endpoint")`,
		`case "changeDb1_1":`,
		`unstructured.SetNestedField(obj.Object, "db-1.internal", "status", "endpoint")`,
		`case "deliverCache":`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated source lacks %q", want)
		}
	}
	if strings.Contains(text, "environmentCycle") || strings.Contains(text, `case "changeDb1":`) {
		t.Error("per-value environment cycles values")
	}
	// The DatabaseInstance declares two fields but no value domain.
	if _, err := adaptergen.GenerateReaderValueEnvironment(fixture.Package, a.Spec, []string{"inst1"}, nil); err == nil || !strings.Contains(err.Error(), "exactly one declared field with a value domain") {
		t.Fatalf("object without a domain: %v", err)
	}
	for literal, want := range map[string]string{`"a"`: "changeSource_a", `""`: "changeSource_empty", `"x-1"`: "changeSource_2", "3": "changeSource_3", "true": "changeSource_true"} {
		if got := adaptergen.ChangeValueEvent("source", literal, 2); got != want {
			t.Errorf("ChangeValueEvent(source, %s, 2) = %s, want %s", literal, got, want)
		}
	}
}
