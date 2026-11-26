package goflow

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// A function literal bound once to a local is a helper the reconciler
// declares inline. Typed extraction treats it like a function declaration:
// the binding adds no node, and each call of the local inlines the body
// with the parameters bound to the call's arguments. A return in the body
// assigns the call's results to the locals the call assigns and continues
// after the call. A field selection rooted at a parameter that is bound to
// an object local, or to its address, resolves on that local.

// inlineFrame is one inlined call: the parameters and the argument bound to
// each, the locals the call's results are assigned to ("" or "_" for a
// discarded result) and the node that follows the call.
type inlineFrame struct {
	params  map[*types.Var]ast.Expr
	targets []string
	next    int
	call    *ast.CallExpr
}

// closureStatement handles the two statements of a local closure: its
// binding and a call of it. It reports false for any other statement.
func (e *extractor) closureStatement(s ast.Stmt, next int) (int, bool, error) {
	if x, ok := s.(*ast.AssignStmt); ok && x.Tok == token.DEFINE && len(x.Lhs) == 1 && len(x.Rhs) == 1 && isFuncLit(x.Rhs[0]) {
		lit := ast.Unparen(x.Rhs[0]).(*ast.FuncLit)
		id, ok := x.Lhs[0].(*ast.Ident)
		if !ok {
			return 0, false, nil
		}
		obj, ok := e.info.Defs[id].(*types.Var)
		if !ok {
			return 0, false, nil
		}
		if e.assignments(obj) > 1 {
			return 0, true, e.diagnose(s, "closure", fmt.Sprintf("closure %s is reassigned; a closure extracts only when it is bound once", id.Name))
		}
		if lit.Type.Params != nil {
			for _, f := range lit.Type.Params.List {
				if f.Names == nil {
					return 0, true, e.diagnose(f, "closure", "closure parameters must be named")
				}
				if _, variadic := f.Type.(*ast.Ellipsis); variadic {
					return 0, true, e.diagnose(f, "closure", "variadic closure parameters are not supported")
				}
			}
		}
		return next, true, nil
	}
	call := statementCall(s)
	if call == nil {
		return 0, false, nil
	}
	fn, ok := ast.Unparen(call.Fun).(*ast.Ident)
	if !ok {
		return 0, false, nil
	}
	obj, ok := e.info.Uses[fn].(*types.Var)
	if !ok {
		return 0, false, nil
	}
	lit, ok := e.closures[obj]
	if !ok {
		return 0, false, nil
	}
	for _, f := range e.frames {
		if f.call == call {
			return 0, true, e.diagnose(s, "closure", "recursive closure call")
		}
	}
	id, err := e.inline(lit, call, s, next)
	return id, true, err
}

// assignments counts the assignments and definitions of obj in the method.
func (e *extractor) assignments(obj *types.Var) int {
	n := 0
	ast.Inspect(e.method, func(node ast.Node) bool {
		x, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range x.Lhs {
			id, ok := ast.Unparen(lhs).(*ast.Ident)
			if !ok {
				continue
			}
			if e.info.Defs[id] == obj || e.info.Uses[id] == obj {
				n++
			}
		}
		return true
	})
	return n
}

// inline lowers the body of lit as the statement s, which calls it,
// binding each parameter to its argument and each result to the local the
// statement assigns it to.
func (e *extractor) inline(lit *ast.FuncLit, call *ast.CallExpr, s ast.Stmt, next int) (int, error) {
	frame := inlineFrame{params: map[*types.Var]ast.Expr{}, next: next, call: call}
	var names []*ast.Ident
	if lit.Type.Params != nil {
		for _, f := range lit.Type.Params.List {
			names = append(names, f.Names...)
		}
	}
	if len(names) != len(call.Args) {
		return 0, e.diagnose(call, "closure", fmt.Sprintf("closure takes %d parameters, called with %d arguments", len(names), len(call.Args)))
	}
	for i, name := range names {
		param, ok := e.info.Defs[name].(*types.Var)
		if !ok {
			return 0, e.diagnose(name, "closure", "closure parameter without type information")
		}
		frame.params[param] = call.Args[i]
	}
	results := 0
	if lit.Type.Results != nil {
		for _, f := range lit.Type.Results.List {
			results += max(1, len(f.Names))
		}
	}
	if x, ok := s.(*ast.AssignStmt); ok {
		if x.Tok != token.DEFINE && x.Tok != token.ASSIGN {
			return 0, e.diagnose(s, "closure", "unsupported assignment of a closure result")
		}
		if len(x.Lhs) != results {
			return 0, e.diagnose(s, "closure", fmt.Sprintf("closure returns %d results, assigned to %d locals", results, len(x.Lhs)))
		}
		for _, lhs := range x.Lhs {
			id, ok := ast.Unparen(lhs).(*ast.Ident)
			if !ok {
				return 0, e.diagnose(lhs, "closure", "a closure result must be assigned to a local")
			}
			target := id.Name
			if t, ok := e.opts.Targets[id.Name]; ok {
				target = t
			}
			if id.Name != "_" {
				if _, ok := e.opts.Scope[target]; !ok {
					return 0, e.diagnose(lhs, "closure", "closure result local "+id.Name+" is not a model symbol")
				}
			}
			frame.targets = append(frame.targets, target)
		}
	}
	e.frames = append(e.frames, frame)
	defer func() { e.frames = e.frames[:len(e.frames)-1] }()
	return e.block(lit.Body.List, next)
}

// inlineReturn lowers a return inside an inlined closure: an assignment of
// the results to the call's locals that continues after the call. A call
// that discards the results, an expression statement, assigns nothing.
func (e *extractor) inlineReturn(x *ast.ReturnStmt, frame inlineFrame) (int, error) {
	if len(frame.targets) != 0 && len(x.Results) != len(frame.targets) {
		return 0, e.diagnose(x, "closure", fmt.Sprintf("closure returns %d results, the call assigns %d", len(x.Results), len(frame.targets)))
	}
	updates := map[string]expr.Expression{}
	for i, result := range x.Results {
		if i >= len(frame.targets) || frame.targets[i] == "_" {
			continue
		}
		target := frame.targets[i]
		v, err := e.expression(result, e.opts.Scope[target].Type)
		if err != nil {
			return 0, err
		}
		updates[target] = v
	}
	if len(updates) == 0 {
		return frame.next, nil
	}
	return e.add(Node{Kind: "assign", Updates: updates}, x, frame.next, 0), nil
}

// boundArgument returns the argument bound to a closure parameter in the
// innermost frame that binds it.
func (e *extractor) boundArgument(obj types.Object) (ast.Expr, bool) {
	v, ok := obj.(*types.Var)
	if !ok {
		return nil, false
	}
	for i := len(e.frames) - 1; i >= 0; i-- {
		if arg, ok := e.frames[i].params[v]; ok {
			return arg, true
		}
	}
	return nil, false
}

// boundRoot resolves the local a field selection rooted at a closure
// parameter stands for: the identifier the argument names, or its address,
// followed through nested closures. It reports false for a root that is no
// bound parameter.
func (e *extractor) boundRoot(root *ast.Ident) (string, bool) {
	name, found := "", false
	for {
		obj, ok := e.info.Uses[root].(*types.Var)
		if !ok {
			return name, found
		}
		arg, ok := e.boundArgument(obj)
		if !ok {
			return name, found
		}
		x := ast.Unparen(arg)
		if u, ok := x.(*ast.UnaryExpr); ok && u.Op == token.AND {
			x = ast.Unparen(u.X)
		}
		id, ok := x.(*ast.Ident)
		if !ok {
			return name, found
		}
		name, found = id.Name, true
		root = id
	}
}

// collectClosures records every function literal bound to a local by a
// definition in the method, before lowering: statements lower last-first,
// so a call is lowered before the binding it uses.
func (e *extractor) collectClosures() {
	e.closures = map[*types.Var]*ast.FuncLit{}
	ast.Inspect(e.method, func(node ast.Node) bool {
		x, ok := node.(*ast.AssignStmt)
		if !ok || x.Tok != token.DEFINE || len(x.Lhs) != 1 || len(x.Rhs) != 1 {
			return true
		}
		lit, ok := ast.Unparen(x.Rhs[0]).(*ast.FuncLit)
		if !ok {
			return true
		}
		id, ok := x.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		if obj, ok := e.info.Defs[id].(*types.Var); ok {
			e.closures[obj] = lit
		}
		return true
	})
}

func isFuncLit(x ast.Expr) bool {
	_, ok := ast.Unparen(x).(*ast.FuncLit)
	return ok
}
