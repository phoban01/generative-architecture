package goflow

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// A range over a list lowers to a loop: an index the frontend introduces,
// a head that tests it against the list's size, the element bound at the
// index, the body, and an increment back to the head; continue jumps to
// the increment.
func TestRangeOverListLowersToLoop(t *testing.T) {
	l := loadFixture(t, "./pkg/goflow/testdata/typed", "Controller", "Count")
	scope := map[string]expr.Symbol{"items": {Type: expr.ListOfString, Quint: "items"}, "skip": {Type: "string", Quint: "skip"}, "n": {Type: "int", Quint: "n"}, "x": {Type: "string", Quint: "x"}}
	p, err := ExtractTyped(l, Options{Scope: scope, Returns: []Return{{Type: "int"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Locals["x_index"]; got.Type != "int" {
		t.Fatalf("the loop index was not introduced: %+v", p.Locals)
	}
	byID := map[int]Node{}
	for _, n := range p.Nodes {
		byID[n.ID] = n
	}
	// n := 0, then the index starts at 0 and the head tests it.
	entry := byID[p.Entry]
	if entry.Kind != "assign" || entry.Updates["n"] != "0" {
		t.Fatalf("entry %+v", entry)
	}
	init := byID[entry.Next]
	if init.Kind != "assign" || init.Updates["x_index"] != "0" {
		t.Fatalf("loop init %+v", init)
	}
	head := byID[init.Next]
	if head.Kind != "branch" || head.Expression != "(x_index < size(items))" {
		t.Fatalf("loop head %+v", head)
	}
	bind := byID[head.Next]
	if bind.Kind != "assign" || bind.Updates["x"] != "items[x_index]" {
		t.Fatalf("element binding %+v", bind)
	}
	skipTest := byID[bind.Next]
	if skipTest.Kind != "branch" || skipTest.Expression != "(x == skip)" {
		t.Fatalf("body %+v", skipTest)
	}
	increment := byID[skipTest.Next]
	if increment.Kind != "assign" || increment.Updates["x_index"] != "(x_index + 1)" || increment.Next != head.ID {
		t.Fatalf("continue does not reach the increment that returns to the head: %+v", increment)
	}
	count := byID[skipTest.Else]
	if count.Kind != "assign" || count.Updates["n"] != "(n + 1)" || count.Next != increment.ID {
		t.Fatalf("body assignment %+v", count)
	}
	exit := byID[head.Else]
	if exit.Kind != "return" || exit.Expression != "n" {
		t.Fatalf("loop exit %+v", exit)
	}
}

// append lowers to a concatenation, len to size, break to the exit, and a
// list declared without a value starts at the model's initial list.
func TestListBuiltinsAndBreak(t *testing.T) {
	l := loadFixture(t, "./pkg/goflow/testdata/typed", "Controller", "Keep")
	scope := map[string]expr.Symbol{"items": {Type: expr.ListOfString, Quint: "items"}, "want": {Type: "string", Quint: "want"}, "kept": {Type: expr.ListOfString, Quint: "kept"}, "x": {Type: "string", Quint: "x"}, "i": {Type: "int", Quint: "i"}}
	p, err := ExtractTyped(l, Options{Scope: scope, Returns: []Return{{Type: "int"}}})
	if err != nil {
		t.Fatal(err)
	}
	var appended, returned, broke bool
	for _, n := range p.Nodes {
		if n.Kind == "assign" && n.Updates["kept"] == "(kept + [items[i]])" {
			appended = true
		}
		if n.Kind == "return" && n.Expression == "size(kept)" {
			returned = true
		}
		if n.Kind == "branch" && n.Expression == "(x != want)" {
			// break jumps to the exit: the return.
			if next := p.Nodes[n.Next]; next.Kind == "return" {
				broke = true
			}
		}
	}
	if !appended || !returned || !broke {
		t.Fatalf("appended=%v returned=%v broke=%v\n%+v", appended, returned, broke, p.Nodes)
	}
	if _, introduced := p.Locals["i"]; introduced {
		t.Fatalf("the declared index was introduced again: %+v", p.Locals)
	}
	// A range over something that is not a list is a diagnostic.
	_, err = ExtractTyped(l, Options{Scope: map[string]expr.Symbol{"items": {Type: "string", Quint: "items"}, "want": {Type: "string", Quint: "want"}, "kept": {Type: expr.ListOfString, Quint: "kept"}, "x": {Type: "string", Quint: "x"}, "i": {Type: "int", Quint: "i"}}, Returns: []Return{{Type: "int"}}})
	if d, ok := AsDiagnostic(err); !ok || !strings.Contains(d.Reason, "list(string)") {
		t.Fatalf("expected a type diagnostic for the range, got %v", err)
	}
}

// A conversion between integer types lowers to its operand.
func TestIntegerConversionIsTransparent(t *testing.T) {
	l := loadFixture(t, "./pkg/goflow/testdata/typed", "Controller", "Trim")
	scope := map[string]expr.Symbol{"items": {Type: expr.ListOfString, Quint: "items"}, "limit": {Type: "int", Quint: "limit"}, "n": {Type: "int", Quint: "n"}, "i": {Type: "int", Quint: "i"}}
	p, err := ExtractTyped(l, Options{Scope: scope, Returns: []Return{{Type: "int"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range p.Nodes {
		if n.Kind == "branch" && n.Expression == "(i >= (size(items) - limit))" {
			return
		}
	}
	t.Fatalf("no branch on the converted comparison:\n%+v", p.Nodes)
}
