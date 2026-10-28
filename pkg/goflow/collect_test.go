package goflow

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// Extraction continues past an unsupported statement and returns every
// located diagnostic in source order. The result stays failed.
func TestCollectsEveryDiagnostic(t *testing.T) {
	scope := map[string]expr.Symbol{"n": {Type: "int", Quint: "n"}, "ready": {Type: "bool", Quint: "ready"}}
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{
			name: "two statements",
			body: "for {\n\t}\n\tgo work()\n\treturn true",
			want: []string{"for:4", "go:6"},
		},
		{
			name: "nested and last",
			body: "if ready {\n\t\tdefer work()\n\t\treturn false\n\t}\n\tgo work()\n\treturn n",
			want: []string{"defer:5", "go:8", "expression:9"},
		},
		{
			name: "condition and body",
			body: "if n {\n\t\tgo work()\n\t}\n\treturn true",
			want: []string{"expression:4", "go:5"},
		},
		{
			name: "fallthrough is kept",
			body: "go work()\n\tif ready {\n\t\treturn true\n\t}",
			want: []string{"go:4", "fallthrough:5"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\n\nfunc F(n int, ready bool) bool {\n\t" + tc.body + "\n}\n"
			p, err := Extract([]byte(src), Options{File: "f.go", Method: "F", Scope: scope, Returns: []Return{{Type: "bool"}}})
			if p != nil {
				t.Fatalf("failed extraction returned a program: %+v", p)
			}
			ds, ok := AsDiagnostics(err)
			if !ok {
				t.Fatalf("no diagnostics: %v", err)
			}
			var got []string
			for _, d := range ds {
				got = append(got, d.Construct+":"+strconv.Itoa(d.Line))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("diagnostics %v, want %v\n%v", got, tc.want, err)
			}
			if first, ok := AsDiagnostic(err); !ok || first != ds[0] {
				t.Fatalf("AsDiagnostic does not find the first diagnostic: %v", first)
			}
		})
	}
}
