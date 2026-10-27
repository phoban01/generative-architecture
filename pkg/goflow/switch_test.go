package goflow

import (
	"github.com/phoban01/generative-architecture/pkg/expr"
	"testing"
)

func switchOptions(method string) Options {
	return Options{File: "switch.go", Method: method, Scope: map[string]expr.Symbol{"phase": {Type: "string", Quint: "phase"}, "n": {Type: "int", Quint: "n"}}, Returns: []Return{{Type: "int"}}}
}

func TestTaggedSwitchLowersToBranchChain(t *testing.T) {
	src := []byte(`package p

func F(phase string) int {
	switch phase {
	case "ready":
		return 1
	case "waiting", "pending":
		return 2
	default:
		return 0
	}
}
`)
	p, err := Extract(src, switchOptions("F"))
	if err != nil {
		t.Fatal(err)
	}
	ready := p.Nodes[p.Entry]
	waiting := p.Nodes[ready.Else]
	pending := p.Nodes[waiting.Else]
	fallback := p.Nodes[pending.Else]
	for _, c := range []struct {
		node Node
		want expr.Expression
		line int
	}{{ready, `(phase == "ready")`, 5}, {waiting, `(phase == "waiting")`, 7}, {pending, `(phase == "pending")`, 7}} {
		if c.node.Kind != "branch" || c.node.Expression != c.want || c.node.Line != c.line {
			t.Fatalf("unexpected case branch %+v, want %s at line %d", c.node, c.want, c.line)
		}
	}
	if p.Nodes[ready.Next].Expression != "1" || p.Nodes[waiting.Next].Expression != "2" || waiting.Next != pending.Next {
		t.Fatalf("case bodies not shared or lost: %+v", p.Nodes)
	}
	if fallback.Kind != "return" || fallback.Expression != "0" {
		t.Fatalf("default clause lost: %+v", fallback)
	}
}

func TestTaglessSwitchLowersCaseExpressions(t *testing.T) {
	src := []byte(`package p

func G(n int) int {
	switch {
	case n > 10:
		return 2
	case n > 0:
		return 1
	}
	return 0
}
`)
	p, err := Extract(src, switchOptions("G"))
	if err != nil {
		t.Fatal(err)
	}
	big := p.Nodes[p.Entry]
	small := p.Nodes[big.Else]
	if big.Expression != "(n > 10)" || small.Expression != "(n > 0)" {
		t.Fatalf("case expressions not lowered: %+v %+v", big, small)
	}
	after := p.Nodes[small.Else]
	if after.Kind != "return" || after.Expression != "0" {
		t.Fatalf("control after the switch lost: %+v", after)
	}
	if p.Nodes[big.Next].Expression != "2" || p.Nodes[small.Next].Expression != "1" {
		t.Fatalf("case bodies lost: %+v", p.Nodes)
	}
}

func TestTypedSwitchLowersConstantCases(t *testing.T) {
	p, err := ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Controller", "Score"), switchOptions("Score"))
	if err != nil {
		t.Fatal(err)
	}
	ready := p.Nodes[p.Entry]
	waiting := p.Nodes[ready.Else]
	if ready.Expression != `(phase == "ready")` || waiting.Expression != `(phase == "waiting")` {
		t.Fatalf("constant cases not lowered: %+v %+v", ready, waiting)
	}
}

func TestUnsupportedSwitchesAreDiagnosed(t *testing.T) {
	for name, c := range map[string]struct {
		src       string
		construct string
		line      int
	}{
		"fallthrough": {"package p\n\nfunc H(phase string) int {\n\tswitch phase {\n\tcase \"a\":\n\t\tfallthrough\n\tcase \"b\":\n\t\treturn 1\n\t}\n\treturn 0\n}\n", "fallthrough", 6},
		"non-literal": {"package p\n\nfunc H(phase string, n int) int {\n\tswitch phase {\n\tcase phase + \"x\":\n\t\treturn 1\n\t}\n\treturn 0\n}\n", "case", 5},
		"type switch": {"package p\n\nfunc H(v any) int {\n\tswitch v.(type) {\n\tcase string:\n\t\treturn 1\n\t}\n\treturn 0\n}\n", "switch", 4},
	} {
		_, err := Extract([]byte(c.src), switchOptions("H"))
		d, ok := AsDiagnostic(err)
		if !ok || d.Construct != c.construct || d.Line != c.line {
			t.Fatalf("%s: expected %s diagnostic at line %d, got %v", name, c.construct, c.line, err)
		}
	}
}
