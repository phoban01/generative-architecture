package watchgen_test

import (
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/watchgen"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) ([]byte, map[string]effect.Resource) {
	t.Helper()
	source, err := os.ReadFile("../../examples/assignment/wiring.go")
	if err != nil {
		t.Fatal(err)
	}
	resources := map[string]effect.Resource{}
	for _, kind := range []string{"InfraMachine", "MachineClaim"} {
		resources["generative-architecture/examples/machinevendor/api/v1alpha1."+kind] = effect.Resource{Group: "test.example", Version: "v1", Kind: kind}
	}
	return source, resources
}
func TestExtractWatchAndFieldMapping(t *testing.T) {
	source, resources := fixture(t)
	result, err := watchgen.Extract(source, "MAC", "SetupWithManager", resources)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rules) != 2 || result.Rules[0].Mapping != "self" || strings.Join(result.Rules[1].NamePath, ".") != "Spec.InfraMachineName" || result.Rules[1].MappingSHA256 == "" {
		t.Fatalf("wrong extracted rules: %+v", result)
	}
	changed := strings.ReplaceAll(string(source), "claim.Spec.InfraMachineName", "claim.Name")
	result, err = watchgen.Extract([]byte(changed), "MAC", "SetupWithManager", resources)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.Rules[1].NamePath, ".") != "Name" {
		t.Fatal("mapper field was hardcoded")
	}
}
func TestGlobalPredicatesAndUnsupportedSetup(t *testing.T) {
	source, resources := fixture(t)
	predicateSource := strings.Replace(string(source), "import (", "import (\n predicate \"sigs.k8s.io/controller-runtime/pkg/predicate\"", 1)
	predicateSource = strings.Replace(predicateSource, "Complete(r)", "WithEventFilter(predicate.ResourceVersionChangedPredicate{}).Complete(r)", 1)
	result, err := watchgen.Extract([]byte(predicateSource), "MAC", "SetupWithManager", resources)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range result.Rules {
		if len(rule.Predicates) != 1 || rule.Predicates[0] != "resourceVersionChanged" {
			t.Fatal("global predicate omitted")
		}
	}
	for _, changed := range []string{
		strings.Replace(string(source), "Complete(r)", "Complete(other)", 1),
		strings.Replace(string(source), "NewControllerManagedBy(mgr)", "NewControllerManagedBy(other)", 1),
		strings.Replace(predicateSource, "ResourceVersionChangedPredicate", "Funcs", 1),
		strings.Replace(string(source), "Complete(r)", "WithOptions(customOptions).Complete(r)", 1),
		strings.Replace(string(source), "return ctrl.NewControllerManagedBy(mgr).", "if enabled { return nil };return ctrl.NewControllerManagedBy(mgr).", 1),
		strings.Replace(string(source), "sigs.k8s.io/controller-runtime/pkg/handler", "untrusted.example/handler", 1),
		strings.Replace(string(source), "claim.Spec.InfraMachineName == \"\"", "claim.Spec.InfraMachineName != \"\"", 1),
	} {
		if _, err = watchgen.Extract([]byte(changed), "MAC", "SetupWithManager", resources); err == nil {
			t.Fatalf("unsupported wiring accepted: %s", changed)
		}
	}
}

func TestUnstructuredWatchGVKAndRuntimeBinding(t *testing.T) {
	source, resources := fixture(t)
	extract := func(s string) (watchgen.Result, error) {
		return watchgen.Extract([]byte(s), "MAC", "SetupWithManager", resources)
	}
	result, err := extract(string(source))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Rules[0].Unstructured || result.Rules[0].Resource.Kind != "InfraMachine" {
		t.Fatal("unstructured resource lost its GVK")
	}
	changed, err := extract(strings.Replace(string(source), `Kind: "InfraMachine"`, `Kind: "OtherMachine"`, 1))
	if err != nil {
		t.Fatal(err)
	}
	if changed.Rules[0].Resource.Kind != "OtherMachine" {
		t.Fatal("GVK was pinned to old type")
	}
	literal := `schema.GroupVersionKind{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "InfraMachine"}`
	dynamic := strings.Replace(string(source), literal, "r.InfraGVK", 1)
	if dynamic == string(source) {
		t.Fatal("mutation missed source")
	}
	if _, err = extract(dynamic); err == nil {
		t.Fatal("unresolved runtime GVK accepted")
	}
	bindings := map[string]effect.Resource{"r.InfraGVK": {Group: "provider.example", Version: "v2", Kind: "ProviderMachine"}}
	result, err = watchgen.ExtractWithGVKs([]byte(dynamic), "MAC", "SetupWithManager", resources, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rules[0].Resource != bindings["r.InfraGVK"] {
		t.Fatal("runtime binding ignored")
	}
	if _, err = extract(strings.Replace(string(source), `Kind: "InfraMachine"`, `Kind: ""`, 1)); err == nil {
		t.Fatal("missing GVK accepted")
	}
}
