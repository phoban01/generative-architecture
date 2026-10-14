package goflow

import (
	"generative-architecture/pkg/expr"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"
)

// fixtures caches type-checked test packages so each is loaded once per run.
var fixtures = struct {
	sync.Mutex
	loaded map[string]*packages.Package
}{loaded: map[string]*packages.Package{}}

// loadFixture locates receiver.method in the package at pattern (relative to
// the module root) and returns it with its type information.
func loadFixture(t *testing.T, pattern, receiver, method string) Loaded {
	t.Helper()
	fixtures.Lock()
	defer fixtures.Unlock()
	p, ok := fixtures.loaded[pattern]
	if !ok {
		l, err := Load(moduleRoot(t), Selector{Package: pattern, Receiver: receiver, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		p = l.Package
		fixtures.loaded[pattern] = p
	}
	for _, f := range p.Syntax {
		if fn := FindMethod(f, receiver, method); fn != nil {
			return Loaded{Package: p, File: f, Func: fn, Path: p.Fset.Position(fn.Pos()).Filename}
		}
	}
	t.Fatalf("method %s.%s not found in %s", receiver, method, pattern)
	return Loaded{}
}

func TestExtractTypedLowersConstantsToLiterals(t *testing.T) {
	l := loadFixture(t, "./pkg/goflow/testdata/typed", "Controller", "Retry")
	p, err := ExtractTyped(l, Options{Scope: map[string]expr.Symbol{"attempts": {Type: "int", Quint: "attempts"}}, Returns: []Return{{Type: "bool"}}})
	if err != nil {
		t.Fatal(err)
	}
	entry := p.Nodes[p.Entry]
	if entry.Kind != "branch" || entry.Expression != "(attempts >= 3)" {
		t.Fatalf("constant not lowered: %+v", entry)
	}
	if p.SourceSHA256 == "" || p.MethodSHA256 == "" {
		t.Fatalf("provenance hashes missing: %+v", p)
	}
	// An identifier outside Scope is a located diagnostic, not a silent name.
	_, err = ExtractTyped(l, Options{Returns: []Return{{Type: "bool"}}})
	d, ok := AsDiagnostic(err)
	if !ok || d.Construct != "identifier" || d.Line == 0 {
		t.Fatalf("expected identifier diagnostic, got %v", err)
	}
	// Text mode is unchanged: the constant is an unknown name.
	if _, err := Extract([]byte("package p\nconst maxRetries = 3\nfunc F(attempts int) bool { if attempts >= maxRetries { return false }; return true }"), Options{Method: "F", Scope: map[string]expr.Symbol{"attempts": {Type: "int", Quint: "attempts"}}, Returns: []Return{{Type: "bool"}}}); err == nil {
		t.Fatal("text mode resolved a constant without type information")
	}
}
