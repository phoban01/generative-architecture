package dynamicgen_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/dynamicgen"
	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
)

func TestExtractDynamicResourceDependenciesAndRejectDrift(t *testing.T) {
	source, err := os.ReadFile("../../examples/database/dynamic_store.go")
	if err != nil {
		t.Fatal(err)
	}
	var catalog dynamicgen.Catalog
	if _, err = krm.Read("../../examples/database/resources.yaml", "KubernetesResourceCatalog", &catalog); err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err = krm.Read("../../examples/database/generated/model.yaml", "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	methods := map[string]string{"DatabaseExists": "readDatabase", "DatabaseInstance": "readInstance", "CreateDatabaseInstance": "createInstance", "UpdateDatabaseStatus": "publish"}
	extract := func(code string) (dynamicgen.Result, error) {
		return dynamicgen.Extract([]byte(code), "DynamicStore", "s.Client", methods, catalog)
	}
	result, err := extract(string(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accesses) != 4 {
		t.Fatalf("missing accesses: %+v", result)
	}
	if err = result.CheckEffects(spec.Effects); err != nil {
		t.Fatal(err)
	}
	changed, err := extract(strings.Replace(string(source), ".UpdateStatus(", ".Update(", 1))
	if err != nil {
		t.Fatal(err)
	}
	effects := result.IR("database", "dynamic_store.go")
	for _, e := range effects {
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
		if e.ID == "publish" && (e.Subresource != "status" || e.Operation != "update" || e.Provenance.Source.Line == 0) {
			t.Fatalf("publish effect %+v", e)
		}
	}
	if reflect.DeepEqual(changed.IR("database", "dynamic_store.go"), effects) {
		t.Fatal("source change did not change the IR effects")
	}
	if err = changed.CheckEffects(spec.Effects); err == nil {
		t.Fatal("changed API verb retained old effect summary")
	}
	changed, err = extract(strings.Replace(string(source), "Resource(s.DatabaseResource)", "Resource(s.InstanceResource)", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err = changed.CheckEffects(spec.Effects); err == nil {
		t.Fatal("wrong resource retained old summary")
	}
	for _, code := range []string{
		strings.Replace(string(source), "Resource(s.DatabaseResource)", "Resource(discover())", 1),
		strings.Replace(string(source), ".UpdateStatus(", ".Unsupported(", 1),
		strings.Replace(string(source), "obj, err := s.Client.Resource(s.DatabaseResource)", "alias := s.Client; obj, err := alias.Resource(s.DatabaseResource)", 1),
	} {
		if _, err = extract(code); err == nil {
			t.Fatal("unresolved or unsupported client operation accepted")
		}
	}
}

// storeSource is a small dynamic store whose methods each make one call.
const storeSource = `package store

func (s *Store) ListPeople() {
	s.Client.Resource(s.People).Namespace(s.Namespace).List(s.Context, metav1.ListOptions{LabelSelector: "app=x"})
}

func (s *Store) DeletePerson() {
	s.Client.Resource(s.People).Namespace(s.Namespace).Delete(s.Context, s.Name, metav1.DeleteOptions{})
}

func (s *Store) PatchPerson() {
	s.Client.Resource(s.People).Namespace(s.Namespace).Patch(s.Context, s.Name, types.MergePatchType, s.Data, metav1.PatchOptions{})
}

func (s *Store) ApplyPatchPerson() {
	s.Client.Resource(s.People).Namespace(s.Namespace).Patch(s.Context, s.Name, types.ApplyPatchType, s.Data, metav1.PatchOptions{FieldManager: "x"})
}

func (s *Store) ApplyPerson() {
	s.Client.Resource(s.People).Namespace(s.Namespace).Apply(s.Context, s.Name, s.Object, metav1.ApplyOptions{FieldManager: "x"})
}

func (s *Store) ApplyPersonStatus() {
	s.Client.Resource(s.People).Namespace(s.Namespace).ApplyStatus(s.Context, s.Name, s.Object, metav1.ApplyOptions{FieldManager: "x"})
}
`

func peopleCatalog() dynamicgen.Catalog {
	return dynamicgen.Catalog{Bindings: []dynamicgen.Binding{{Expression: "s.People", Resource: effect.Resource{Group: "example.org", Version: "v1", Kind: "Person"}, Plural: "people", Scope: "Namespaced"}}}
}

func TestExtractDynamicListAndDelete(t *testing.T) {
	methods := map[string]string{"ListPeople": "listPeople", "DeletePerson": "deletePerson"}
	result, err := dynamicgen.Extract([]byte(storeSource), "Store", "s.Client", methods, peopleCatalog())
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]string{}
	for _, a := range result.Accesses {
		operations[a.Method] = a.Operation
	}
	if operations["ListPeople"] != "list" || operations["DeletePerson"] != "delete" || len(result.Accesses) != 2 {
		t.Fatalf("wrong accesses: %+v", result.Accesses)
	}
	person := effect.Resource{Group: "example.org", Version: "v1", Kind: "Person"}
	if err := result.CheckEffects([]effect.Descriptor{{ID: "listPeople", Resource: person, Operation: "list", Consistency: "direct"}, {ID: "deletePerson", Resource: person, Operation: "delete", Consistency: "direct"}}); err != nil {
		t.Fatal(err)
	}
	withSubresource := strings.Replace(storeSource, "metav1.DeleteOptions{})", `metav1.DeleteOptions{}, "status")`, 1)
	if _, err := dynamicgen.Extract([]byte(withSubresource), "Store", "s.Client", methods, peopleCatalog()); err == nil {
		t.Fatal("delete with a subresource accepted")
	}
}

func TestExtractDynamicPatchAndApply(t *testing.T) {
	methods := map[string]string{"PatchPerson": "patch", "ApplyPatchPerson": "applyPatch", "ApplyPerson": "apply", "ApplyPersonStatus": "applyStatus"}
	result, err := dynamicgen.Extract([]byte(storeSource), "Store", "s.Client", methods, peopleCatalog())
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]string{}
	for _, a := range result.Accesses {
		operations[a.Method] = a.Operation
	}
	want := map[string]string{"PatchPerson": "patch", "ApplyPatchPerson": "apply", "ApplyPerson": "apply", "ApplyPersonStatus": "status.apply"}
	for method, op := range want {
		if operations[method] != op {
			t.Fatalf("%s: got operation %q, want %q", method, operations[method], op)
		}
	}
	variable := strings.Replace(storeSource, "types.MergePatchType", "s.PatchType", 1)
	_, err = dynamicgen.Extract([]byte(variable), "Store", "s.Client", map[string]string{"PatchPerson": "patch"}, peopleCatalog())
	if d, ok := goflow.AsDiagnostic(err); !ok || d.Construct != "Patch" {
		t.Fatalf("expected a located Patch diagnostic, got %v", err)
	}
}

func TestCatalogRejectsAmbiguousDiscovery(t *testing.T) {
	a := dynamicgen.Binding{Expression: "a", Resource: effect.Resource{Group: "test", Version: "v1", Kind: "A"}, Plural: "items", Scope: "Namespaced"}
	b := a
	b.Expression = "b"
	b.Resource.Kind = "B"
	if _, err := (dynamicgen.Catalog{Bindings: []dynamicgen.Binding{a, b}}).Mapper(); err == nil {
		t.Fatal("ambiguous resource accepted")
	}
}

func TestUnsupportedDynamicOperationIsLocated(t *testing.T) {
	source, err := os.ReadFile("../../examples/database/dynamic_store.go")
	if err != nil {
		t.Fatal(err)
	}
	var catalog dynamicgen.Catalog
	if _, err = krm.Read("../../examples/database/resources.yaml", "KubernetesResourceCatalog", &catalog); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(source), "\n")
	line := 0
	for i, l := range lines {
		if strings.Contains(l, ".UpdateStatus(") {
			lines[i] = strings.Replace(l, ".UpdateStatus(", ".Patch(", 1)
			line = i + 1
		}
	}
	if line == 0 {
		t.Fatal("fixture has no UpdateStatus call")
	}
	methods := map[string]string{"UpdateDatabaseStatus": "publish"}
	_, err = dynamicgen.Extract([]byte(strings.Join(lines, "\n")), "DynamicStore", "s.Client", methods, catalog)
	d, ok := goflow.AsDiagnostic(err)
	if !ok {
		t.Fatalf("expected a diagnostic, got %T: %v", err, err)
	}
	if d.Construct != "Patch" || d.Line != line || d.File != "dynamic_store.go" {
		t.Fatalf("unexpected diagnostic %+v (want Patch at line %d)", d, line)
	}
}
