package adaptergen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/adaptergen/internal/fixture"
)

func TestFromSpec(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	ps, err := adaptergen.FromSpec(a.Spec, "db1")
	if err != nil {
		t.Fatal(err)
	}
	var states, readers []string
	for _, p := range ps {
		states = append(states, p.State)
		readers = append(readers, p.Reader)
	}
	want := []string{"Database_db1_exists", "Database_db1_status_endpoint", "cached_Database_db1_exists", "cached_Database_db1_status_endpoint", "DatabaseInstance_inst1_exists", "DatabaseInstance_inst1_spec_databaseName", "DatabaseInstance_inst1_status_endpoint"}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("projections %v, want %v", states, want)
	}
	wantReaders := []string{"api", "api", "cache", "cache", "api", "api", "api"}
	if !reflect.DeepEqual(readers, wantReaders) {
		t.Fatalf("readers %v, want %v", readers, wantReaders)
	}
	// Without cached objects the API projections are FromAbstraction's.
	plain, err := adaptergen.FromSpec(a.Spec)
	if err != nil {
		t.Fatal(err)
	}
	for i := range plain {
		plain[i].Reader = ""
	}
	if !reflect.DeepEqual(plain, adaptergen.FromAbstraction(a)) {
		t.Fatalf("FromSpec %+v differs from FromAbstraction", plain)
	}
	for name, cached := range map[string][]string{"unknown object": {"db9"}, "listed twice": {"db1", "db1"}} {
		if _, err := adaptergen.FromSpec(a.Spec, cached...); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestGenerateReaderProjection(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	ps, err := adaptergen.FromSpec(a.Spec, "db1")
	if err != nil {
		t.Fatal(err)
	}
	src, err := adaptergen.GenerateReaderProjection(fixture.Package, ps)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "reader_projection", src)
	p := typecheck(t, src)
	params, results := signature(t, p, "Project")
	wantParams := []string{"context.Context", "sigs.k8s.io/controller-runtime/pkg/client.Reader", "sigs.k8s.io/controller-runtime/pkg/client.Reader"}
	if !reflect.DeepEqual(params, wantParams) {
		t.Errorf("Project takes %v, want only API readers %v", params, wantParams)
	}
	if !reflect.DeepEqual(results, []string{"map[string]any", "error"}) {
		t.Errorf("Project returns %v", results)
	}
	assertNoModelState(t, p)

	// Without cached objects the cache reader is not a parameter.
	src, err = adaptergen.GenerateReaderProjection(fixture.Package, adaptergen.FromAbstraction(a))
	if err != nil {
		t.Fatal(err)
	}
	params, _ = signature(t, typecheck(t, src), "Project")
	if len(params) != 2 {
		t.Fatalf("API-only projection takes %v", params)
	}
}

func TestReaderProjectionRejections(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	ps, err := adaptergen.FromSpec(a.Spec, "db1")
	if err != nil {
		t.Fatal(err)
	}
	// A dynamic-client projection has no cache reader.
	if _, err := adaptergen.GenerateProjection(fixture.Package, ps); err == nil || !strings.Contains(err.Error(), "API only") {
		t.Fatalf("cached projection accepted by the dynamic generator: %v", err)
	}
	bad := append([]adaptergen.Projection(nil), ps...)
	bad[0].Reader = "elsewhere"
	if _, err := adaptergen.GenerateReaderProjection(fixture.Package, bad); err == nil || !strings.Contains(err.Error(), "unknown reader") {
		t.Fatalf("unknown reader accepted: %v", err)
	}
}
