package goflow

import (
	"generative-architecture/pkg/expr"
	"os"
	"path/filepath"
	"testing"
)

func TestExternalSelectionAndUnsupportedCode(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{"go.mod": "module example.org/upstream\n\ngo 1.25\n", "controller.go": `package upstream
type Controller struct{}
func (Controller) Check(err error) bool { if err != nil {return false}; return true }`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Select(root, Selector{Package: ".", Receiver: "Controller", Method: "Check"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Extract(s.Source, Options{Receiver: "Controller", Method: "Check", Bindings: map[string]expr.Expression{"err": "failed"}, Errors: map[string]bool{"err": true}, Scope: map[string]expr.Symbol{"failed": {Type: "bool", Quint: "failed"}}, Returns: []Return{{Type: "bool"}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Nodes[p.Entry].Expression != "failed" {
		t.Fatalf("error binding lost: %+v", p)
	}
	if _, err := Extract([]byte(`package p;func F() bool {for { };return true}`), Options{Method: "F", Returns: []Return{{Type: "bool"}}}); err == nil {
		t.Fatal("loop accepted")
	}
	if _, err := VerifyDeclarations([]byte(`package p;func helper() bool{return false}`), map[string]string{"helper": "func helper() bool{return true}"}); err == nil {
		t.Fatal("helper drift accepted")
	}
}

func TestDiagnosticLocatesUnsupportedLoop(t *testing.T) {
	src := []byte("package p\n\nfunc (c Controller) F() bool {\n\t// comment\n\tfor {\n\t}\n\treturn true\n}\n")
	_, err := Extract(src, Options{File: "controller.go", Receiver: "Controller", Method: "F", Returns: []Return{{Type: "bool"}}})
	if err == nil {
		t.Fatal("loop accepted")
	}
	d, ok := AsDiagnostic(err)
	if !ok {
		t.Fatalf("expected *Diagnostic, got %T: %v", err, err)
	}
	if d.Line != 5 || d.File != "controller.go" || d.Construct != "for" {
		t.Fatalf("unexpected diagnostic %+v", d)
	}
	item := d.Item()
	if item.Location.Line != 5 || item.Kind != "for" || item.Reason == "" {
		t.Fatalf("unexpected item %+v", item)
	}
	if _, ok := AsDiagnostic(os.ErrNotExist); ok {
		t.Fatal("plain error treated as diagnostic")
	}
}
