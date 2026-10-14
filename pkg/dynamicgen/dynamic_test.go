package dynamicgen_test

import (
	"os"
	"strings"
	"testing"

	"generative-architecture/pkg/dynamicgen"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
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
	if _, err = krm.Read("../../examples/database/model.yaml", "TransitionModel", &spec); err != nil {
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
	if changed.LikeC4() == result.LikeC4() {
		t.Fatal("source change did not change diagram")
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
