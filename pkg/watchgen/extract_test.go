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

func TestWatchesOwnerAndObjectHandlers(t *testing.T) {
	source, resources := fixture(t)
	extract := func(s string) (watchgen.Result, error) {
		return watchgen.Extract([]byte(s), "DatabaseReconciler", "SetupWithManager", resources)
	}
	const mapped = "Watches(&apiv1.DatabaseInstance{}, handler.EnqueueRequestsFromMapFunc(InstanceToDatabase))"
	if !strings.Contains(string(source), mapped) {
		t.Fatal("fixture changed")
	}
	owner := strings.Replace(string(source), mapped, "Watches(&apiv1.DatabaseInstance{}, handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &apiv1.Database{}, handler.OnlyControllerOwner()))", 1)
	result, err := extract(owner)
	if err != nil {
		t.Fatal(err)
	}
	rule := result.Rules[1]
	if rule.Mapping != "owner" || rule.OwnerKind != "Database" || !rule.OnlyController || rule.Owner == nil || rule.Owner.Group != "test.example" || rule.Resource.Kind != "DatabaseInstance" {
		t.Fatalf("unexpected owner rule %+v", rule)
	}
	w := result.IR("database", "wiring.go")[1]
	if w.Mapping.Kind != ir.MappingOwner || w.Mapping.OwnerGVK == nil || w.Mapping.OwnerGVK.Kind != "Database" || w.Mapping.OwnerGVK.Group != "test.example" || !w.Mapping.OnlyController {
		t.Fatalf("owner mapping lost in IR: %+v", w)
	}
	// Without OnlyControllerOwner every owner reference is enqueued.
	result, err = extract(strings.Replace(owner, ", handler.OnlyControllerOwner()", "", 1))
	if err != nil {
		t.Fatal(err)
	}
	if rule := result.Rules[1]; rule.Mapping != "owner" || rule.OwnerKind != "Database" || rule.OnlyController {
		t.Fatalf("unexpected owner rule %+v", rule)
	}
	if w := result.IR("database", "wiring.go")[1]; w.Mapping.OnlyController {
		t.Fatalf("IR mapping should enqueue every owner: %+v", w)
	}
	// The declared unstructured primary may also name the owner.
	result, err = extract(strings.Replace(owner, "&apiv1.Database{}", "database", 1))
	if err != nil {
		t.Fatal(err)
	}
	if rule := result.Rules[1]; rule.Mapping != "owner" || rule.Owner == nil || rule.Owner.Group != "demo.architecture.local" {
		t.Fatalf("unexpected owner rule %+v", rule)
	}
	// EnqueueRequestForObject maps the watched object to itself.
	result, err = extract(strings.Replace(string(source), mapped, "Watches(&apiv1.DatabaseInstance{}, handler.EnqueueRequestForObject{})", 1))
	if err != nil {
		t.Fatal(err)
	}
	if rule := result.Rules[1]; rule.Mapping != "self" || strings.Join(rule.NamePath, ".") != "Name" {
		t.Fatalf("unexpected object rule %+v", rule)
	}
	for _, changed := range []string{
		strings.Replace(owner, "mgr.GetScheme()", "scheme", 1),
		strings.Replace(owner, "mgr.GetRESTMapper()", "mgr.GetCache()", 1),
		strings.Replace(owner, "handler.OnlyControllerOwner()", "handler.OnlyControllerOwner(x)", 1),
		strings.Replace(owner, "handler.OnlyControllerOwner()", "customOption", 1),
		strings.Replace(owner, "&apiv1.Database{}", "&apiv1.Unknown{}", 1),
		strings.Replace(owner, "&apiv1.Database{}", "&apiv1.Database{}, handler.OnlyControllerOwner()", 1),
		strings.Replace(string(source), mapped, "Watches(&apiv1.DatabaseInstance{}, handler.EnqueueRequestForObject{Custom: true})", 1),
		strings.Replace(string(source), mapped, "Watches(&apiv1.DatabaseInstance{}, customHandler)", 1),
	} {
		if _, err := extract(changed); err == nil {
			t.Fatalf("unsupported handler accepted: %s", changed)
		}
	}
	_, err = extract(strings.Replace(owner, "mgr.GetScheme()", "scheme", 1))
	if d, ok := goflow.AsDiagnostic(err); !ok || d.Construct != "handler" {
		t.Fatalf("expected a handler diagnostic, got %v", err)
	}
}

func TestWithOptionsLiteralExtraction(t *testing.T) {
	source, resources := fixture(t)
	withImport := strings.Replace(string(source), "import (", "import (\n controller \"sigs.k8s.io/controller-runtime/pkg/controller\"", 1)
	extract := func(options string) (watchgen.Result, error) {
		return watchgen.Extract([]byte(strings.Replace(withImport, "Complete(r)", "WithOptions("+options+").Complete(r)", 1)), "DatabaseReconciler", "SetupWithManager", resources)
	}
	result, err := extract("controller.Options{MaxConcurrentReconciles: 4, RateLimiter: r.Limiter, NeedLeaderElection: true}")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Options) != 3 || result.Options["maxConcurrentReconciles"] != "4" || result.Options["rateLimiter"] != "true" || result.Options["needLeaderElection"] != "true" {
		t.Fatalf("unexpected options %+v", result.Options)
	}
	if len(result.Rules) != 2 {
		t.Fatalf("options changed the rules: %+v", result.Rules)
	}
	result, err = extract("controller.Options{}")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Options) != 0 {
		t.Fatalf("unexpected options %+v", result.Options)
	}
	for field, options := range map[string]string{
		"MaxConcurrentReconciles": "controller.Options{MaxConcurrentReconciles: r.Workers}",
		"CacheSyncTimeout":        "controller.Options{MaxConcurrentReconciles: 1, CacheSyncTimeout: 2 * time.Minute}",
		"RecoverPanic":            "controller.Options{RecoverPanic: ptr.To(true)}",
	} {
		_, err := extract(options)
		d, ok := goflow.AsDiagnostic(err)
		if !ok || d.Construct != "WithOptions" || !strings.Contains(d.Reason, field) {
			t.Fatalf("expected a WithOptions diagnostic naming %s, got %v", field, err)
		}
	}
	for _, options := range []string{
		"controller.Options{MaxConcurrentReconciles: \"4\"}",
		"controller.Options{4}",
		"controller.Options{MaxConcurrentReconciles: 1, MaxConcurrentReconciles: 2}",
		"customOptions",
		"controller.Options{}, controller.Options{}",
		"controller.Options{}).WithOptions(controller.Options{}",
	} {
		if _, err := extract(options); err == nil {
			t.Fatalf("unsupported options accepted: %s", options)
		}
	}
}

func TestWatchesRawSourceKind(t *testing.T) {
	source, resources := fixture(t)
	withImport := strings.Replace(string(source), "import (", "import (\n source \"sigs.k8s.io/controller-runtime/pkg/source\"\n predicate \"sigs.k8s.io/controller-runtime/pkg/predicate\"", 1)
	extract := func(raw string) (watchgen.Result, error) {
		return watchgen.Extract([]byte(strings.Replace(withImport, "Complete(r)", "WatchesRawSource("+raw+").Complete(r)", 1)), "DatabaseReconciler", "SetupWithManager", resources)
	}
	result, err := extract("source.Kind(mgr.GetCache(), &apiv1.DatabaseInstance{}, handler.TypedEnqueueRequestForObject[*apiv1.DatabaseInstance]{}, predicate.TypedGenerationChangedPredicate[*apiv1.DatabaseInstance]{})")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rules) != 3 {
		t.Fatalf("expected three rules, got %+v", result.Rules)
	}
	rule := result.Rules[2]
	if rule.Mapping != "self" || rule.Resource.Kind != "DatabaseInstance" || strings.Join(rule.NamePath, ".") != "Name" || strings.Join(rule.Predicates, ";") != "generationChanged" || rule.Line == 0 {
		t.Fatalf("unexpected raw source rule %+v", rule)
	}
	if w := result.IR("database", "wiring.go")[2]; w.Primary || w.Mapping.Kind != ir.MappingSelf || w.Resource.Kind != "DatabaseInstance" {
		t.Fatalf("raw source watch lost in IR: %+v", w)
	}
	// The untyped handler forms of Watches apply to raw sources as well.
	result, err = extract("source.Kind(mgr.GetCache(), &apiv1.DatabaseInstance{}, handler.EnqueueRequestsFromMapFunc(InstanceToDatabase))")
	if err != nil {
		t.Fatal(err)
	}
	if rule := result.Rules[2]; rule.Mapping != "InstanceToDatabase" || strings.Join(rule.NamePath, ".") != "Spec.DatabaseName" {
		t.Fatalf("unexpected mapped raw source rule %+v", rule)
	}
	result, err = extract("source.Kind(mgr.GetCache(), &apiv1.DatabaseInstance{}, handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &apiv1.Database{}, handler.OnlyControllerOwner()))")
	if err != nil {
		t.Fatal(err)
	}
	if rule := result.Rules[2]; rule.Mapping != "owner" || rule.OwnerKind != "Database" || !rule.OnlyController {
		t.Fatalf("unexpected owner raw source rule %+v", rule)
	}
	for construct, raw := range map[string]string{
		"WatchesRawSource": "source.Channel(events, handler.EnqueueRequestForObject{})",
		"handler":          "source.Kind(mgr.GetCache(), &apiv1.DatabaseInstance{}, handler.TypedEnqueueRequestForObject[*apiv1.Database]{})",
	} {
		_, err := extract(raw)
		d, ok := goflow.AsDiagnostic(err)
		if !ok || d.Construct != construct {
			t.Fatalf("expected a %s diagnostic for %s, got %v", construct, raw, err)
		}
	}
	for _, raw := range []string{
		"source.Kind(cache, &apiv1.DatabaseInstance{}, handler.TypedEnqueueRequestForObject[*apiv1.DatabaseInstance]{})",
		"source.Kind(mgr.GetCache(), &apiv1.DatabaseInstance{})",
		"source.Kind(mgr.GetCache(), &apiv1.DatabaseInstance{}, handler.TypedEnqueueRequestForObject[apiv1.DatabaseInstance]{})",
		"source.Kind(mgr.GetCache(), &apiv1.DatabaseInstance{}, handler.TypedEnqueueRequestForObject[*apiv1.DatabaseInstance]{Custom: true})",
		"source.Kind(mgr.GetCache(), &apiv1.DatabaseInstance{}, handler.TypedEnqueueRequestsFromMapFunc[*apiv1.DatabaseInstance, reconcile.Request](mapper))",
		"source.Kind(mgr.GetCache(), &apiv1.DatabaseInstance{}, customHandler)",
		"source.Kind(mgr.GetCache(), &apiv1.DatabaseInstance{}, handler.TypedEnqueueRequestForObject[*apiv1.DatabaseInstance]{}, predicate.Funcs{})",
		"source.Kind(mgr.GetCache(), &apiv1.DatabaseInstance{}, handler.TypedEnqueueRequestForObject[*apiv1.DatabaseInstance]{}, filters...)",
		"source.Kind(mgr.GetCache(), &apiv1.Unknown{}, handler.TypedEnqueueRequestForObject[*apiv1.Unknown]{})",
		"rawSource",
	} {
		if _, err := extract(raw); err == nil {
			t.Fatalf("unsupported raw source accepted: %s", raw)
		}
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
