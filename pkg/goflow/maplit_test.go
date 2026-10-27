package goflow

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// A map literal assigned to a selected map replaces the whole map: with two
// selected keys and a literal that lists one, the other takes its absent
// value instead of keeping its old one.
func TestMapLiteralReplacesSelectedMap(t *testing.T) {
	options := func(modeType string) Options {
		opts := copyOptions()
		opts.Scope["targetMode"] = expr.Symbol{Type: modeType, Quint: "targetMode"}
		field := opts.Field
		opts.Field = func(root string, path []string) (expr.Expression, bool) {
			if root == "target" && strings.Join(path, ".") == "data.mode" {
				return "targetMode", true
			}
			return field(root, path)
		}
		opts.MapKeys = func(root string, path []string) []string {
			if root == "target" && strings.Join(path, ".") == "data" {
				return []string{"value", "mode"}
			}
			return nil
		}
		return opts
	}
	copyFixture := loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Copy")
	assignOf := func(p *Program) Node {
		t.Helper()
		for _, n := range p.Nodes {
			if n.Kind == "assign" {
				return n
			}
		}
		t.Fatalf("no assignment in %+v", p.Nodes)
		return Node{}
	}
	p, err := ExtractTyped(copyFixture, options("string"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := assignOf(p).Updates, map[string]expr.Expression{"targetValue": "sourceValue", "targetMode": `""`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("updates %v, want %v", got, want)
	}
	p, err = ExtractTyped(copyFixture, options("int"))
	if err != nil {
		t.Fatal(err)
	}
	if got := assignOf(p).Updates["targetMode"]; got != "0" {
		t.Fatalf("absent int %q", got)
	}
	// Without MapKeys only the listed key is updated.
	opts := options("string")
	opts.MapKeys = nil
	if p, err = ExtractTyped(copyFixture, opts); err != nil {
		t.Fatal(err)
	}
	if got := assignOf(p).Updates; len(got) != 1 {
		t.Fatalf("updates without MapKeys %v", got)
	}

	// A selected key the field callback does not map, and a type with no
	// absent value, are located diagnostics.
	opts = options("string")
	opts.MapKeys = func(string, []string) []string { return []string{"other", "value"} }
	_, err = ExtractTyped(copyFixture, opts)
	if d, ok := AsDiagnostic(err); !ok || d.Construct != "field" || d.Line == 0 || !strings.Contains(d.Reason, "target.data.other") {
		t.Fatalf("unmapped selected key accepted: %v", err)
	}
	_, err = ExtractTyped(copyFixture, options("record"))
	if d, ok := AsDiagnostic(err); !ok || d.Construct != "assign" || !strings.Contains(d.Reason, "no absent value") {
		t.Fatalf("type without absent value accepted: %v", err)
	}
}
