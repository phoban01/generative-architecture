package watchgen_test

import (
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/watchgen"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) ([]byte, map[string]effect.Resource) {
	t.Helper()
	source, err := os.ReadFile("../../examples/database/wiring.go")
	if err != nil {
		t.Fatal(err)
	}
	resources := map[string]effect.Resource{}
	for _, kind := range []string{"Database", "DatabaseInstance"} {
		resources["generative-architecture/examples/database/api/v1alpha1."+kind] = effect.Resource{Group: "test.example", Version: "v1", Kind: kind}
	}
	return source, resources
}
func TestExtractWatchAndFieldMapping(t *testing.T) {
	source, resources := fixture(t)
	result, err := watchgen.Extract(source, "DatabaseReconciler", "SetupWithManager", resources)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rules) != 2 || result.Rules[0].Mapping != "self" || strings.Join(result.Rules[1].NamePath, ".") != "Spec.DatabaseName" || result.Rules[1].MappingSHA256 == "" {
		t.Fatalf("wrong extracted rules: %+v", result)
	}
	changed := strings.ReplaceAll(string(source), "instance.Spec.DatabaseName", "instance.Name")
	result, err = watchgen.Extract([]byte(changed), "DatabaseReconciler", "SetupWithManager", resources)
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
	result, err := watchgen.Extract([]byte(predicateSource), "DatabaseReconciler", "SetupWithManager", resources)
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
		strings.Replace(string(source), "instance.Spec.DatabaseName == \"\"", "instance.Spec.DatabaseName != \"\"", 1),
	} {
		if _, err = watchgen.Extract([]byte(changed), "DatabaseReconciler", "SetupWithManager", resources); err == nil {
			t.Fatalf("unsupported wiring accepted: %s", changed)
		}
	}
}

func TestUnstructuredWatchGVKAndRuntimeBinding(t *testing.T) {
	source, resources := fixture(t)
	extract := func(s string) (watchgen.Result, error) {
		return watchgen.Extract([]byte(s), "DatabaseReconciler", "SetupWithManager", resources)
	}
	result, err := extract(string(source))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Rules[0].Unstructured || result.Rules[0].Resource.Kind != "Database" {
		t.Fatal("unstructured resource lost its GVK")
	}
	changed, err := extract(strings.Replace(string(source), `Kind: "Database"`, `Kind: "OtherKind"`, 1))
	if err != nil {
		t.Fatal(err)
	}
	if changed.Rules[0].Resource.Kind != "OtherKind" {
		t.Fatal("GVK was pinned to old type")
	}
	literal := `schema.GroupVersionKind{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "Database"}`
	dynamic := strings.Replace(string(source), literal, "r.DatabaseGVK", 1)
	if dynamic == string(source) {
		t.Fatal("mutation missed source")
	}
	if _, err = extract(dynamic); err == nil {
		t.Fatal("unresolved runtime GVK accepted")
	}
	bindings := map[string]effect.Resource{"r.DatabaseGVK": {Group: "provider.example", Version: "v2", Kind: "ProviderDatabase"}}
	result, err = watchgen.ExtractWithGVKs([]byte(dynamic), "DatabaseReconciler", "SetupWithManager", resources, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rules[0].Resource != bindings["r.DatabaseGVK"] {
		t.Fatal("runtime binding ignored")
	}
	if _, err = extract(strings.Replace(string(source), `Kind: "Database"`, `Kind: ""`, 1)); err == nil {
		t.Fatal("missing GVK accepted")
	}
}

func TestUnsupportedBuilderOperationIsLocated(t *testing.T) {
	source, resources := fixture(t)
	lines := strings.Split(string(source), "\n")
	line := 0
	for i, l := range lines {
		if strings.Contains(l, "Complete(r)") {
			lines[i] = strings.Replace(l, "Complete(r)", "Owns(&apiv1.DatabaseInstance{}).Complete(r)", 1)
			line = i + 1
		}
	}
	if line == 0 {
		t.Fatal("fixture has no Complete(r)")
	}
	_, err := watchgen.Extract([]byte(strings.Join(lines, "\n")), "DatabaseReconciler", "SetupWithManager", resources)
	d, ok := goflow.AsDiagnostic(err)
	if !ok {
		t.Fatalf("expected a diagnostic, got %T: %v", err, err)
	}
	if d.Construct != "Owns" || d.Line != line || d.File != "wiring.go" {
		t.Fatalf("unexpected diagnostic %+v (want Owns at line %d)", d, line)
	}
}
