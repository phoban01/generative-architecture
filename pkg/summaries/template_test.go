package summaries

import (
	"strings"
	"testing"

	"generative-architecture/pkg/expr"
)

// bareBindings binds every placeholder of a summary to its own name without
// the $, so templates become plain CEL over variables of those names.
func bareBindings(s Summary) map[string]string {
	b := map[string]string{}
	for _, p := range s.Bindings {
		b[p] = strings.TrimPrefix(p, "$")
	}
	return b
}

// evalTemplate substitutes bare bindings into a template and evaluates it
// with pkg/expr, so a template that does not parse as CEL fails the test.
func evalTemplate(t *testing.T, template string, bindings map[string]string, vars map[string]any) any {
	t.Helper()
	source, err := Substitute(template, bindings)
	if err != nil {
		t.Fatal(err)
	}
	e, err := expr.NewEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.Eval(expr.Expression(source), vars)
	if err != nil {
		t.Fatalf("%s: %v", source, err)
	}
	return v
}

// guardsParse evaluates every guard and update of the summary against vars.
func guardsParse(t *testing.T, s Summary, vars map[string]any) {
	t.Helper()
	b := bareBindings(s)
	for _, o := range s.Outcomes {
		if v := evalTemplate(t, o.Guard, b, vars); v != true && v != false {
			t.Fatalf("%s/%s %s: guard %q is not boolean: %v", s.Operation, s.Consistency, o.Name, o.Guard, v)
		}
		for target, value := range o.Updates {
			evalTemplate(t, value, b, vars)
			if _, err := Substitute(target, b); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func outcomeNames(s Summary) []string {
	out := make([]string, 0, len(s.Outcomes))
	for _, o := range s.Outcomes {
		out = append(out, o.Name)
	}
	return out
}

func equalNames(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
