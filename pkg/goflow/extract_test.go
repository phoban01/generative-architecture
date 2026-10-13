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
