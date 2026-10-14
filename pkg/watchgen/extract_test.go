package watchgen_test

import (
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
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
			lines[i] = strings.Replace(l, "Complete(r)", "WithLogConstructor(nil).Complete(r)", 1)
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
	if d.Construct != "WithLogConstructor" || d.Line != line || d.File != "wiring.go" {
		t.Fatalf("unexpected diagnostic %+v (want WithLogConstructor at line %d)", d, line)
	}
}

func TestOwnsMapsToPrimaryOwner(t *testing.T) {
	source, resources := fixture(t)
	extract := func(s string) (watchgen.Result, error) {
		return watchgen.Extract([]byte(s), "DatabaseReconciler", "SetupWithManager", resources)
	}
	withImport := strings.Replace(string(source), "import (", "import (\n builder \"sigs.k8s.io/controller-runtime/pkg/builder\"\n predicate \"sigs.k8s.io/controller-runtime/pkg/predicate\"", 1)
	owns := strings.Replace(withImport, "Complete(r)", "Owns(&apiv1.DatabaseInstance{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).Complete(r)", 1)
	result, err := extract(owns)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rules) != 3 {
		t.Fatalf("expected three rules, got %+v", result.Rules)
	}
	rule := result.Rules[2]
	if rule.Mapping != "owner" || rule.OwnerKind != "Database" || rule.Resource.Kind != "DatabaseInstance" || !rule.OnlyController || len(rule.NamePath) != 0 {
		t.Fatalf("unexpected owner rule %+v", rule)
	}
	if rule.Owner == nil || *rule.Owner != result.Rules[0].Resource {
		t.Fatalf("owner resource is not the primary resource: %+v", rule)
	}
	if len(rule.Predicates) != 1 || rule.Predicates[0] != "generationChanged" {
		t.Fatalf("owner watch predicates lost: %+v", rule)
	}
	watches := result.IR("database", "wiring.go")
	w := watches[2]
	if w.Primary || w.Mapping.Kind != ir.MappingOwner || w.Mapping.OwnerGVK == nil || w.Mapping.OwnerGVK.Kind != "Database" || w.Mapping.OwnerGVK.Group != "demo.architecture.local" {
		t.Fatalf("owner mapping lost in IR: %+v", w)
	}
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	// Owns before For has no owner kind yet; unsupported options and objects fail.
	for _, changed := range []string{
		strings.Replace(string(source), "For(database).", "Owns(&apiv1.DatabaseInstance{}).For(database).", 1),
		strings.Replace(string(source), "Complete(r)", "Owns(&apiv1.DatabaseInstance{}, builder.MatchEveryOwner).Complete(r)", 1),
		strings.Replace(string(source), "Complete(r)", "Owns(&apiv1.DatabaseInstance{Spec: spec}).Complete(r)", 1),
		strings.Replace(string(source), "Complete(r)", "Owns().Complete(r)", 1),
	} {
		if _, err := extract(changed); err == nil {
			t.Fatalf("unsupported Owns accepted: %s", changed)
		}
	}
	_, err = extract(strings.Replace(string(source), "For(database).", "Owns(&apiv1.DatabaseInstance{}).For(database).", 1))
	if d, ok := goflow.AsDiagnostic(err); !ok || d.Construct != "Owns" {
		t.Fatalf("expected an Owns diagnostic, got %v", err)
	}
}

func TestNamedRecordsControllerName(t *testing.T) {
	source, resources := fixture(t)
	extract := func(s string) (watchgen.Result, error) {
		return watchgen.Extract([]byte(s), "DatabaseReconciler", "SetupWithManager", resources)
	}
	result, err := extract(string(source))
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "" {
		t.Fatalf("unexpected controller name %q", result.Name)
	}
	named := strings.Replace(string(source), "For(database).", "Named(\"tenant\").For(database).", 1)
	result, err = extract(named)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "tenant" {
		t.Fatalf("expected controller name tenant, got %q", result.Name)
	}
	watches := result.IR("", "wiring.go")
	if len(watches) != 2 || watches[0].Controller != "tenant" || watches[1].Controller != "tenant" {
		t.Fatalf("controller name lost in IR: %+v", watches)
	}
	if explicit := result.IR("explicit", "wiring.go"); explicit[0].Controller != "explicit" {
		t.Fatalf("explicit controller name overridden: %+v", explicit[0])
	}
	for _, changed := range []string{
		strings.Replace(string(source), "For(database).", "Named(name).For(database).", 1),
		strings.Replace(string(source), "For(database).", "Named(\"a\" + \"b\").For(database).", 1),
		strings.Replace(string(source), "For(database).", "Named(\"\").For(database).", 1),
		strings.Replace(string(source), "For(database).", "Named(\"a\", \"b\").For(database).", 1),
		strings.Replace(string(source), "For(database).", "Named(\"a\").Named(\"b\").For(database).", 1),
	} {
		_, err := extract(changed)
		d, ok := goflow.AsDiagnostic(err)
		if !ok || d.Construct != "Named" {
			t.Fatalf("expected a Named diagnostic, got %v for %s", err, changed)
		}
	}
}
