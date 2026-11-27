package goflow

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// A range over a list lowers to a loop in the process graph (ADR-0011): an
// index local the frontend introduces, a head that tests the index against
// the list's size, a binding of the element local to the element at the
// index, the body, and an increment that returns to the head. continue
// jumps to the increment and break to the exit. Lists are sublists of a
// bounded population, so every loop terminates.

// loopFrame is one range loop being lowered: where continue and break go.
type loopFrame struct {
	continueTo, breakTo int
}

// rangeStmt lowers for key, value := range list.
func (e *extractor) rangeStmt(x *ast.RangeStmt, next int) (int, error) {
	if x.Tok != token.DEFINE && x.Tok != token.ILLEGAL {
		return 0, e.diagnose(x, "range", "range must define its loop variables")
	}
	listExpr, err := e.expression(x.X, expr.ListOfString)
	if err != nil {
		return 0, err
	}
	list := string(listExpr)
	index, err := e.loopLocal(x.Key, "int", x.Value, "_index")
	if err != nil {
		return 0, err
	}
	element, err := e.loopLocal(x.Value, "string", nil, "")
	if err != nil {
		return 0, err
	}
	// The head's successor is the binding, which the body's lowering
	// decides; it is patched once the body exists.
	head := e.add(Node{Kind: "branch", Expression: expr.Expression("(" + index + " < size(" + list + "))")}, x, -1, next)
	increment := e.add(Node{Kind: "assign", Updates: map[string]expr.Expression{index: expr.Expression("(" + index + " + 1)")}}, x, head, 0)
	e.loops = append(e.loops, loopFrame{continueTo: increment, breakTo: next})
	body, err := e.block(x.Body.List, increment)
	e.loops = e.loops[:len(e.loops)-1]
	if err != nil {
		return 0, err
	}
	entry := body
	if element != "" {
		entry = e.add(Node{Kind: "assign", Updates: map[string]expr.Expression{element: expr.Expression(list + "[" + index + "]")}}, x, body, 0)
	}
	e.program.Nodes[head].Next = entry
	return e.add(Node{Kind: "assign", Updates: map[string]expr.Expression{index: "0"}}, x, head, 0), nil
}

// loopLocal names the local a range loop variable lowers to. A named
// variable that is a model symbol keeps its name; a named variable that is
// not, or the blank or absent key, becomes a local the frontend introduces
// (Program.Locals), named after the variable or after the element with the
// suffix. It returns "" for an absent or blank element.
func (e *extractor) loopLocal(v ast.Expr, typ string, element ast.Expr, suffix string) (string, error) {
	name := ""
	if v != nil {
		id, ok := v.(*ast.Ident)
		if !ok {
			return "", e.diagnose(v, "range", "range variables must be identifiers")
		}
		if id.Name != "_" {
			name = id.Name
		}
	}
	if name == "" {
		if suffix == "" {
			return "", nil
		}
		base := "range"
		if id, ok := element.(*ast.Ident); ok && id.Name != "_" {
			base = id.Name
		}
		name = base + suffix
	}
	if s, ok := e.opts.Scope[name]; ok {
		if s.Type != typ {
			return "", e.diagnose(v, "range", fmt.Sprintf("range variable %s has model type %s, want %s", name, s.Type, typ))
		}
		return name, nil
	}
	e.introduce(name, typ)
	return name, nil
}

// introduce declares a local the frontend needs and the abstraction did not
// declare: a loop index or element. The generator declares it as a process
// local reset at each invocation.
func (e *extractor) introduce(name, typ string) {
	if e.program.Locals == nil {
		e.program.Locals = map[string]expr.Symbol{}
	}
	e.program.Locals[name] = expr.Symbol{Type: typ, Quint: name}
	e.opts.Scope[name] = expr.Symbol{Type: typ, Quint: name}
}

// branchStmt lowers continue and break inside a range loop to a jump.
func (e *extractor) branchStmt(x *ast.BranchStmt) (int, bool, error) {
	if x.Tok != token.CONTINUE && x.Tok != token.BREAK {
		return 0, false, nil
	}
	if x.Label != nil {
		return 0, true, e.diagnose(x, "branch", "labelled continue and break are not supported")
	}
	if len(e.loops) == 0 {
		return 0, true, e.diagnose(x, "branch", x.Tok.String()+" outside a range loop")
	}
	frame := e.loops[len(e.loops)-1]
	if x.Tok == token.CONTINUE {
		return frame.continueTo, true, nil
	}
	return frame.breakTo, true, nil
}

// builtinCall lowers len(list) to size(list) and append(list, element) to
// list + [element]. It reports false for any other call.
func (e *extractor) builtinCall(call *ast.CallExpr) (expr.Expression, bool, error) {
	id, ok := ast.Unparen(call.Fun).(*ast.Ident)
	if !ok {
		return "", false, nil
	}
	b, ok := e.info.Uses[id].(*types.Builtin)
	if !ok {
		return "", false, nil
	}
	switch b.Name() {
	case "len":
		if len(call.Args) != 1 {
			return "", true, e.diagnose(call, "expression", "len takes one argument")
		}
		v, err := e.expression(call.Args[0], expr.ListOfString)
		return expr.Expression("size(" + string(v) + ")"), true, err
	case "append":
		if len(call.Args) != 2 || call.Ellipsis != token.NoPos {
			return "", true, e.diagnose(call, "expression", "append of one element to a list is supported")
		}
		list, err := e.expression(call.Args[0], expr.ListOfString)
		if err != nil {
			return "", true, err
		}
		element, err := e.expression(call.Args[1], "string")
		if err != nil {
			return "", true, err
		}
		return expr.Expression("(" + string(list) + " + [" + string(element) + "])"), true, nil
	}
	return "", false, nil
}

// localAssign lowers an assignment of an expression to a model local in
// typed mode: x = v or x := v where x is a model symbol.
func (e *extractor) localAssign(x *ast.AssignStmt, next int) (int, bool, error) {
	if len(x.Lhs) != 1 || len(x.Rhs) != 1 || (x.Tok != token.ASSIGN && x.Tok != token.DEFINE) {
		return 0, false, nil
	}
	id, ok := ast.Unparen(x.Lhs[0]).(*ast.Ident)
	if !ok {
		return 0, false, nil
	}
	_, bound := e.opts.Bindings[Code(x.Rhs[0])]
	if _, isCall := ast.Unparen(x.Rhs[0]).(*ast.CallExpr); isCall && !bound {
		if _, builtin := e.info.Uses[rootIdent(ast.Unparen(x.Rhs[0]).(*ast.CallExpr).Fun)].(*types.Builtin); !builtin {
			return 0, false, nil
		}
	}
	s, ok := e.opts.Scope[id.Name]
	if !ok {
		return 0, false, nil
	}
	v, err := e.expression(x.Rhs[0], s.Type)
	if err != nil {
		return 0, true, err
	}
	return e.add(Node{Kind: "assign", Updates: map[string]expr.Expression{id.Name: v}}, x, next, 0), true, nil
}

// localDecl reports whether s declares a model local without a value, such
// as var kept []string: the local starts at the model's initial value, so
// the declaration adds no node.
func (e *extractor) localDecl(s ast.Stmt) bool {
	d, ok := s.(*ast.DeclStmt)
	if !ok {
		return false
	}
	g, ok := d.Decl.(*ast.GenDecl)
	if !ok || g.Tok != token.VAR || len(g.Specs) != 1 {
		return false
	}
	v, ok := g.Specs[0].(*ast.ValueSpec)
	if !ok || len(v.Names) != 1 || len(v.Values) != 0 {
		return false
	}
	_, declared := e.opts.Scope[v.Names[0].Name]
	return declared
}

// listIndex lowers list[i] for a list-typed base and an int index. It
// reports false when the base is not a list.
func (e *extractor) listIndex(x *ast.IndexExpr) (expr.Expression, bool, error) {
	base, err := e.translate(x.X)
	if err != nil {
		return "", false, nil
	}
	if _, err := e.opts.Compiler.Compile(base, e.opts.Scope, expr.ListOfString); err != nil {
		return "", false, nil
	}
	index, err := e.expression(x.Index, "int")
	if err != nil {
		return "", true, err
	}
	return expr.Expression(string(base) + "[" + string(index) + "]"), true, nil
}
