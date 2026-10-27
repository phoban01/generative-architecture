package watchgen_test

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/watchgen"
)

// extractWithPredicates adds the builder and predicate imports and replaces
// Complete(r) with the given builder tail before extraction.
func extractWithPredicates(t *testing.T, tail string) (watchgen.Result, error) {
	t.Helper()
	source, resources := fixture(t)
	withImport := strings.Replace(string(source), "import (", "import (\n builder \"sigs.k8s.io/controller-runtime/pkg/builder\"\n predicate \"sigs.k8s.io/controller-runtime/pkg/predicate\"", 1)
	return watchgen.Extract([]byte(strings.Replace(withImport, "Complete(r)", tail+".Complete(r)", 1)), "DatabaseReconciler", "SetupWithManager", resources)
}

func TestComposedAndStandardPredicates(t *testing.T) {
	result, err := extractWithPredicates(t, "WithEventFilter(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.And(predicate.LabelChangedPredicate{}, predicate.Not(predicate.AnnotationChangedPredicate{}))))")
	if err != nil {
		t.Fatal(err)
	}
	const want = "or(generationChanged, and(labelChanged, not(annotationChanged)))"
	for _, rule := range result.Rules {
		if len(rule.Predicates) != 1 || rule.Predicates[0] != want {
			t.Fatalf("unexpected predicates %+v", rule.Predicates)
		}
	}
	p := result.IR("database", "wiring.go")[0].Predicates
	if len(p) != 1 || p[0].Kind != "or" || p[0].Args["expression"] != want {
		t.Fatalf("composed predicate lost in IR: %+v", p)
	}
	// Per-watch options accept compositions beside plain predicates.
	result, err = extractWithPredicates(t, "Owns(&apiv1.DatabaseInstance{}, builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}, predicate.Not(predicate.LabelChangedPredicate{})))")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(result.Rules[2].Predicates, ";"); got != "resourceVersionChanged;not(labelChanged)" {
		t.Fatalf("unexpected per-watch predicates %q", got)
	}
	p = result.IR("database", "wiring.go")[2].Predicates
	if len(p) != 2 || p[0].Kind != "resourceVersionChanged" || p[0].Args != nil || p[1].Kind != "not" {
		t.Fatalf("per-watch predicates lost in IR: %+v", p)
	}
	// Typed predicates summarize like their untyped forms.
	result, err = extractWithPredicates(t, "Owns(&apiv1.DatabaseInstance{}, builder.WithPredicates(predicate.TypedGenerationChangedPredicate[*apiv1.DatabaseInstance]{}))")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(result.Rules[2].Predicates, ";"); got != "generationChanged" {
		t.Fatalf("unexpected typed predicate %q", got)
	}
}

func TestUnsupportedPredicatesAreDiagnosed(t *testing.T) {
	for _, tail := range []string{
		"WithEventFilter(predicate.Funcs{})",
		"WithEventFilter(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.Funcs{}))",
		"WithEventFilter(predicate.NewPredicateFuncs(filter))",
		"WithEventFilter(predicate.Not(predicate.NewPredicateFuncs(filter)))",
		"WithEventFilter(predicate.Not())",
		"WithEventFilter(predicate.Not(predicate.GenerationChangedPredicate{}, predicate.LabelChangedPredicate{}))",
		"WithEventFilter(predicate.And())",
		"WithEventFilter(predicate.Or(filters...))",
		"WithEventFilter(predicate.NewTypedPredicateFuncs[*apiv1.Database](filter))",
		"WithEventFilter(predicate.LabelChangedPredicate{Custom: true})",
		"WithEventFilter(customPredicate)",
		"WithEventFilter(other.Or(predicate.GenerationChangedPredicate{}))",
	} {
		_, err := extractWithPredicates(t, tail)
		d, ok := goflow.AsDiagnostic(err)
		if !ok || d.Construct != "predicate" {
			t.Fatalf("expected a predicate diagnostic for %s, got %v", tail, err)
		}
	}
	_, err := extractWithPredicates(t, "WithEventFilter(predicate.Funcs{})")
	if d, _ := goflow.AsDiagnostic(err); d == nil || !strings.Contains(d.Reason, "predicate.Funcs") || d.Line == 0 {
		t.Fatalf("expected a located predicate.Funcs diagnostic, got %v", err)
	}
}
