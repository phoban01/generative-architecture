package goflow

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// String concatenation of constants extracts; any expression the CEL
// compiler rejects is a diagnostic at the Go file and line.
func TestStringConcatenation(t *testing.T) {
	scope := map[string]expr.Symbol{"name": {Type: "string", Quint: "name"}, "ready": {Type: "bool", Quint: "ready"}, "n": {Type: "int", Quint: "n"}}
	for _, tc := range []struct {
		name, body string
		result     expr.Expression
		line       int
		reason     string
	}{
		{name: "constants", body: `return "db" + "-credentials"`, result: `("db" + "-credentials")`},
		{name: "symbol", body: "if ready {\n\t\treturn \"x\"\n\t}\n\treturn name + \"-credentials\"", line: 7, reason: "unsupported string concatenation"},
		{name: "type mismatch", body: `return name + n`, line: 4, reason: "CEL type mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\n\nfunc F(name string, ready bool, n int) string {\n\t" + tc.body + "\n}\n"
			p, err := Extract([]byte(src), Options{File: "controller.go", Method: "F", Scope: scope, Returns: []Return{{Type: "string"}}})
			if tc.reason != "" {
				d, ok := AsDiagnostic(err)
				if !ok || d.File != "controller.go" || d.Line != tc.line || d.Construct != "expression" || !strings.Contains(d.Reason, tc.reason) || strings.Contains(d.Reason, "\n") {
					t.Fatalf("got %v, want a one-line diagnostic at controller.go:%d containing %q", err, tc.line, tc.reason)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := p.Nodes[p.Entry].Expression; got != tc.result {
				t.Fatalf("result %s, want %s", got, tc.result)
			}
		})
	}
}
