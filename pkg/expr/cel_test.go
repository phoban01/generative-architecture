package expr

import (
	"strings"
	"testing"
)

// quantifierScope declares a two-element domain and two boolean states.
func quantifierScope() map[string]Symbol {
	return map[string]Symbol{
		"Keys":    {Type: Domain, Quint: "Keys"},
		"a":       {Type: "bool", Quint: "a"},
		"b":       {Type: "bool", Quint: "b"},
		"counts":  {Type: "map(string, int)", Quint: "counts"},
		"chosen":  {Type: "string", Quint: "chosen"},
		"enabled": {Type: "bool", Quint: "enabled"},
	}
}

// holds is the predicate "the flag of key k is set", over the two keys.
const holds = `((%s == "a" && a) || (%s == "b" && b))`

func on(v string) string { return strings.ReplaceAll(holds, "%s", v) }

func TestLowerQuantifiers(t *testing.T) {
	for _, tc := range []struct{ source, quint string }{
		{`Keys.exists(k, k == chosen)`, `Keys.exists(k => (k == chosen))`},
		{`Keys.all(k, k == chosen)`, `Keys.forall(k => (k == chosen))`},
		{`Keys.exists(x, Keys.all(y, x == y))`, `Keys.exists(x => Keys.forall(y => (x == y)))`},
		{`!Keys.exists(x, Keys.exists(y, x != y))`, `not(Keys.exists(x => Keys.exists(y => (x != y))))`},
	} {
		got, err := Compiler{}.Compile(Expression(tc.source), quantifierScope(), "bool")
		if err != nil {
			t.Fatalf("%s: %v", tc.source, err)
		}
		if got.Quint != tc.quint {
			t.Errorf("%s: got %s, want %s", tc.source, got.Quint, tc.quint)
		}
	}
}

// TestQuantifierTruthTable evaluates exists(), all() and a nested exists()
// with cel-go on every state of two flags over a two-element domain and
// compares each result with the truth table of the Quint lowering:
// Set("a", "b").exists(k => p) is p("a") or p("b"), and forall is and.
func TestQuantifierTruthTable(t *testing.T) {
	scope := quantifierScope()
	exists := Expression(`Keys.exists(k, ` + on("k") + `)`)
	all := Expression(`Keys.all(k, ` + on("k") + `)`)
	pair := Expression(`Keys.exists(x, Keys.exists(y, x != y && ` + on("x") + ` && ` + on("y") + `))`)
	for _, source := range []Expression{exists, all, pair} {
		if _, err := (Compiler{}).Compile(source, scope, "bool"); err != nil {
			t.Fatal(err)
		}
	}
	e, err := NewEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[Expression]map[bool]bool{exists: {}, all: {}, pair: {}}
	for _, a := range []bool{false, true} {
		for _, b := range []bool{false, true} {
			vars := map[string]any{"Keys": []string{"a", "b"}, "a": a, "b": b}
			for source, want := range map[Expression]bool{exists: a || b, all: a && b, pair: a && b} {
				got, err := e.Eval(source, vars)
				if err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("a=%v b=%v: %s is %v in CEL and %v in the Quint truth table", a, b, source, got, want)
				}
				seen[source][want] = true
			}
		}
	}
	for source, outcomes := range seen {
		if !outcomes[true] || !outcomes[false] {
			t.Errorf("%s: truth table does not reach both outcomes", source)
		}
	}
}

func TestQuantifiersRejectUndeclaredCollections(t *testing.T) {
	for _, tc := range []struct{ source, message string }{
		{`Keys.exists(k, k == chosen) && Pods.exists(p, p == chosen)`, `line 1, column 32: exists() ranges over Pods, which is not a declared domain`},
		{"enabled &&\n  counts.exists(k, k == chosen)", `line 2, column 3: exists() requires a declared string domain, got map(string, int)`},
		{`["a", "b"].exists(k, k == chosen)`, `line 1, column 1: unsupported CEL syntax`},
		{`chosen.all(k, k == "a")`, `line 1, column 1: all() requires a declared string domain, got string`},
		{`Keys.exists_one(k, k == chosen)`, `line 1, column 16: only CEL all() and exists() comprehensions are supported`},
		{`Keys.exists(chosen, chosen == "a")`, `CEL binder shadows existing symbol`},
		{`Keys.exists(k, k)`, `exists() predicate must be bool`},
	} {
		_, err := Compiler{}.Compile(Expression(tc.source), quantifierScope(), "bool")
		if err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("%s: got %v, want %q", tc.source, err, tc.message)
		}
	}
}
