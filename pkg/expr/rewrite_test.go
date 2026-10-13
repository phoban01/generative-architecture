package expr

import "testing"

func TestRewriteSymbolsNotLiterals(t *testing.T) {
	rewritten, err := Rewrite(`owner.value == "owner.value" && local == "local"`, map[string]Expression{"owner.value": "shared", "local": "private"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.Eval(rewritten, map[string]any{"shared": "owner.value", "private": "local"})
	if err != nil || v != true {
		t.Fatalf("%s: %v %v", rewritten, v, err)
	}
	if _, err := Rewrite(`[1].exists(x, x == 1)`, nil); err == nil {
		t.Fatal("unsafe binder substitution accepted")
	}
}
