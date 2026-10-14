package goflow

import (
	"generative-architecture/pkg/expr"
	"go/ast"
	"os"
	"reflect"
	"strings"
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

func TestSelectorsResolveToJSONFieldPaths(t *testing.T) {
	var paths [][]string
	opts := Options{Scope: map[string]expr.Symbol{"size": {Type: "int", Quint: "size"}, "kind": {Type: "string", Quint: "kind"}}, Returns: []Return{{Type: "bool"}}}
	opts.Field = func(root string, jsonPath []string) (expr.Expression, bool) {
		paths = append(paths, append([]string{root}, jsonPath...))
		if root != "db" {
			return "", false
		}
		switch strings.Join(jsonPath, ".") {
		case "spec.size":
			return "size", true
		case "kind":
			return "kind", true
		}
		return "", false
	}
	p, err := ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Sized"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Nodes[p.Entry].Expression; got != `((size > 0) && (kind == "Database"))` {
		t.Fatalf("fields not mapped: %s", got)
	}
	if !reflect.DeepEqual(paths, [][]string{{"db", "spec", "size"}, {"db", "kind"}}) {
		t.Fatalf("unexpected paths %v", paths)
	}

	// Map indexing has no field path and stays rejected.
	_, err = ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Value"), opts)
	d, ok := AsDiagnostic(err)
	if !ok || d.Construct != "expression" || d.Source != `cm.Data["value"]` {
		t.Fatalf("map index accepted: %v", err)
	}
	// A field the callback does not map, and one that is never serialized,
	// are located diagnostics.
	opts.Field = func(string, []string) (expr.Expression, bool) { return "", false }
	_, err = ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Sized"), opts)
	if d, ok := AsDiagnostic(err); !ok || d.Construct != "field" || d.Reason != "unmapped field db.spec.size" {
		t.Fatalf("unmapped field accepted: %v", err)
	}
	_, err = ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Hidden"), opts)
	if d, ok := AsDiagnostic(err); !ok || d.Construct != "field" || !strings.Contains(d.Reason, "unexported") {
		t.Fatalf("unexported field accepted: %v", err)
	}
}

func TestCallResolvesCalleeBySymbol(t *testing.T) {
	l := loadFixture(t, "./examples/database", "", "Reconcile")
	scope := map[string]expr.Symbol{}
	for name, typ := range map[string]string{"found": "bool", "compatible": "bool", "instanceFound": "bool", "endpoint": "string"} {
		scope[name] = expr.Symbol{Type: typ, Quint: name}
	}
	kinds := map[Callee]string{
		{Package: "generative-architecture/examples/database", Receiver: "Store", Method: "DatabaseExists"}:                "readDatabase",
		{Package: "generative-architecture/examples/database", Receiver: "Store", Method: "SatisfiesProvisioningContract"}: "checkContract",
		{Package: "generative-architecture/examples/database", Receiver: "Store", Method: "DatabaseInstance"}:              "readInstance",
		{Package: "generative-architecture/examples/database", Receiver: "Store", Method: "CreateDatabaseInstance"}:        "createInstance",
	}
	var seen []Callee
	opts := Options{Scope: scope, Returns: []Return{{Type: "string"}}}
	opts.Call = func(c Callee, call *ast.CallExpr, convert Convert) (Node, bool, error) {
		seen = append(seen, c)
		if kind, ok := kinds[c]; ok {
			return Node{Kind: kind}, true, nil
		}
		if c.Receiver == "Store" && c.Method == "UpdateDatabaseStatus" && len(call.Args) == 1 {
			value, err := convert(call.Args[0], "string")
			return Node{Kind: "publish", Expression: value}, true, err
		}
		return Node{}, false, nil
	}
	typed, err := ExtractTyped(l, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 5 || seen[2] != (Callee{Package: "generative-architecture/examples/database", Receiver: "Store", Method: "DatabaseInstance"}) {
		t.Fatalf("unexpected callees %+v", seen)
	}
	var instance *Node
	for i := range typed.Nodes {
		if typed.Nodes[i].Source == "instanceFound, endpoint := store.DatabaseInstance()" {
			instance = &typed.Nodes[i]
		}
	}
	if instance == nil || instance.Kind != "readInstance" {
		t.Fatalf("DatabaseInstance call not mapped by callee: %+v", typed.Nodes)
	}
	if !reflect.DeepEqual(instance.Results, []expr.Expression{"instanceFound", "endpoint"}) {
		t.Fatalf("assignment targets not bound: %+v", instance)
	}
	// Text mode does not bind assignment targets; ignore them when comparing.
	for i := range typed.Nodes {
		if typed.Nodes[i].Kind != "return" {
			typed.Nodes[i].Results = nil
		}
	}
	// The symbol-resolved program matches the text-matched one node for node.
	source, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	text, err := Extract(source, Options{File: l.Path, Method: "Reconcile", Scope: scope, Returns: []Return{{Type: "string"}}, Statement: func(s ast.Stmt, convert Convert) (Node, bool, error) {
		operations := map[string]string{"found := store.DatabaseExists()": "readDatabase", "compatible := store.SatisfiesProvisioningContract()": "checkContract", "instanceFound, endpoint := store.DatabaseInstance()": "readInstance", "store.CreateDatabaseInstance()": "createInstance"}
		if kind, ok := operations[Code(s)]; ok {
			return Node{Kind: kind}, true, nil
		}
		if x, ok := s.(*ast.ExprStmt); ok {
			if call, ok := x.X.(*ast.CallExpr); ok && Code(call.Fun) == "store.UpdateDatabaseStatus" {
				value, err := convert(call.Args[0], "string")
				return Node{Kind: "publish", Expression: value}, true, err
			}
		}
		return Node{}, false, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(typed, text) {
		t.Fatalf("typed and text programs differ:\n%+v\n%+v", typed, text)
	}
}
