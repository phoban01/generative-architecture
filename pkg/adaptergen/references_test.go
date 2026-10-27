package adaptergen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/adaptergen/internal/fixture"
)

func TestGenerateReferenceCheck(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	src, err := adaptergen.GenerateReferenceCheck(fixture.Package, a.Spec)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "references", src)
	p := typecheck(t, src)
	assertNoModelState(t, p)
	params, results := signature(t, p, "CheckReferences")
	wantParams := []string{"context.Context", "k8s.io/client-go/dynamic.Interface", "k8s.io/apimachinery/pkg/api/meta.RESTMapper"}
	if !reflect.DeepEqual(params, wantParams) || !reflect.DeepEqual(results, []string{"error"}) {
		t.Errorf("CheckReferences(%v) %v", params, results)
	}
	want := `"demo", "instance-for-db-1", []string{"spec", "databaseName"}, "Database", []string{"db-1"}, false)`
	if !strings.Contains(string(src), want) {
		t.Errorf("generated check lacks %q:\n%s", want, src)
	}
}

// A referring field whose value domain holds the empty string may be unset.
func TestGenerateReferenceCheckAllowsDeclaredEmpty(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	a.Spec.Resources[1].ValueDomains = map[string][]string{"spec.databaseName": {`""`, `"db-1"`}}
	src, err := adaptergen.GenerateReferenceCheck(fixture.Package, a.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `[]string{"db-1"}, true)`) {
		t.Errorf("generated check does not allow the empty reference:\n%s", src)
	}
}

func TestGenerateReferenceCheckRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*abstraction.Spec)
		want string
	}{
		"no references": {func(s *abstraction.Spec) { s.Resources[1].References = nil }, "no references"},
		"other identity": {func(s *abstraction.Spec) {
			s.Resources[0].Identity = [][]string{{"spec", "uid"}}
		}, "only metadata.name"},
		"other namespace": {func(s *abstraction.Spec) { s.Resources[0].Objects[0].Namespace = "elsewhere" }, "can be named from namespace"},
	} {
		t.Run(name, func(t *testing.T) {
			a, err := fixture.Abstraction()
			if err != nil {
				t.Fatal(err)
			}
			tc.edit(&a.Spec)
			_, err = adaptergen.GenerateReferenceCheck("p", a.Spec)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error containing %q, got %v", tc.want, err)
			}
		})
	}
}
