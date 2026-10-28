package expr

import (
	"strings"
	"testing"
)

func concatScope() map[string]Symbol {
	return map[string]Symbol{
		"a":    {Type: "bool", Quint: "a"},
		"b":    {Type: "bool", Quint: "b"},
		"name": {Type: "string", Quint: "name"},
		"n":    {Type: "int", Quint: "n"},
	}
}

func TestLowerStringConcatenation(t *testing.T) {
	for _, tc := range []struct{ source, quint string }{
		{`"db" + "-credentials"`, `"db-credentials"`},
		{`"a" + "b" + "c"`, `"abc"`},
		{`(a ? "x" : "y") + "-s"`, `(if (a) "x-s" else "y-s")`},
		{`"p-" + (a ? "x" : "y") + (b ? "1" : "2")`, `(if (a) (if (b) "p-x1" else "p-x2") else (if (b) "p-y1" else "p-y2"))`},
		{`("a" + "b") == "ab"`, `("ab" == "ab")`},
	} {
		want := "string"
		if strings.Contains(tc.source, "==") {
			want = "bool"
		}
		got, err := Compiler{}.Compile(Expression(tc.source), concatScope(), want)
		if err != nil {
			t.Fatalf("%s: %v", tc.source, err)
		}
		if got.Quint != tc.quint {
			t.Errorf("%s: got %s, want %s", tc.source, got.Quint, tc.quint)
		}
	}
}

// Each unsupported operand of a concatenation is located in the CEL
// source, and integer addition keeps its own check.
func TestStringConcatenationRejectsUnknownValues(t *testing.T) {
	for _, tc := range []struct{ source, message string }{
		{`name + "-credentials"`, `line 1, column 1: unsupported string concatenation`},
		{`"x-" + name`, `line 1, column 8: unsupported string concatenation`},
		{"\"x\" +\n  (a ? name : \"y\")", `line 2, column 8: unsupported string concatenation`},
		{`"x" + n`, `CEL type mismatch`},
		{`a + b`, `CEL integer type mismatch`},
	} {
		_, err := Compiler{}.Compile(Expression(tc.source), concatScope(), "")
		if err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("%s: got %v, want %q", tc.source, err, tc.message)
		}
	}
}

// TestConcatenationTruthTable evaluates concatenations with cel-go on every
// state of two flags and compares each value with the Quint lowering read
// as a decision table.
func TestConcatenationTruthTable(t *testing.T) {
	source := Expression(`"p-" + (a ? "x" : "y") + (b ? "1" : "2")`)
	got, err := Compiler{}.Compile(source, concatScope(), "string")
	if err != nil {
		t.Fatal(err)
	}
	// The lowering is (if (a) (if (b) "p-x1" else "p-x2") else (if (b) "p-y1" else "p-y2")).
	quint := func(a, b bool) string {
		switch {
		case a && b:
			return "p-x1"
		case a:
			return "p-x2"
		case b:
			return "p-y1"
		}
		return "p-y2"
	}
	if !strings.HasPrefix(got.Quint, `(if (a) (if (b) "p-x1" else "p-x2")`) {
		t.Fatalf("decision table does not match the lowering %s", got.Quint)
	}
	e, err := NewEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []bool{false, true} {
		for _, b := range []bool{false, true} {
			v, err := e.Eval(source, map[string]any{"a": a, "b": b})
			if err != nil {
				t.Fatal(err)
			}
			if v != quint(a, b) {
				t.Errorf("a=%v b=%v: CEL %v, Quint %s", a, b, v, quint(a, b))
			}
		}
	}
}
