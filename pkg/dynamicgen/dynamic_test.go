package dynamicgen_test

import (
	"os"
	"strings"
	"testing"

	"generative-architecture/pkg/dynamicgen"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
)

func TestExtractDynamicResourceDependenciesAndRejectDrift(t *testing.T) {
	source, err := os.ReadFile("../../examples/assignment/dynamic_store.go")
	if err != nil {
		t.Fatal(err)
	}
	var catalog dynamicgen.Catalog
	if _, err = krm.Read("../../examples/assignment/resources.yaml", "KubernetesResourceCatalog", &catalog); err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err = krm.Read("../../examples/assignment/model.yaml", "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	methods := map[string]string{"InfraMachineExists": "readInfra", "MachineClaim": "readClaim", "CreateMachineClaim": "createClaim", "UpdateInfraMachineStatus": "publish"}
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
	changed, err = extract(strings.Replace(string(source), "Resource(s.InfraResource)", "Resource(s.ClaimResource)", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err = changed.CheckEffects(spec.Effects); err == nil {
		t.Fatal("wrong resource retained old summary")
	}
	for _, code := range []string{
		strings.Replace(string(source), "Resource(s.InfraResource)", "Resource(discover())", 1),
		strings.Replace(string(source), ".UpdateStatus(", ".Unsupported(", 1),
		strings.Replace(string(source), "obj, err := s.Client.Resource(s.InfraResource)", "alias := s.Client; obj, err := alias.Resource(s.InfraResource)", 1),
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
