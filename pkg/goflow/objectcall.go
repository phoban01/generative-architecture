package goflow

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/summaries"
)

// objectCall lowers a call to a function with a built-in object summary,
// such as controllerutil.SetControllerReference(owner, obj, scheme), to an
// assign node. Each write the abstraction selects (Options.Field maps it)
// updates its model symbol; the others are listed as unmodelled. A
// selected field the summary gives no value, an unmapped source field a
// selected write needs and an unsummarized option are diagnostics. It
// reports false for any other callee.
func (e *extractor) objectCall(c Callee, call *ast.CallExpr) (Node, bool, error) {
	if c.Receiver != "" || e.opts.Field == nil {
		return Node{}, false, nil
	}
	s, ok := summaries.ObjectSummaryFor(c.Package, c.Method)
	if !ok {
		return Node{}, false, nil
	}
	if err := s.Validate(); err != nil {
		return Node{}, true, err
	}
	if want := max(s.Source, s.Target) + 1 + s.Fixed; len(call.Args) != want {
		return Node{}, true, e.diagnose(call, "call", fmt.Sprintf("%s takes %d arguments in its summary; options are not summarized", c, want))
	}
	source, err := e.objectArg(call.Args[s.Source])
	if err != nil {
		return Node{}, true, err
	}
	target, err := e.objectArg(call.Args[s.Target])
	if err != nil {
		return Node{}, true, err
	}
	for _, path := range s.Unvalued {
		if _, ok := e.opts.Field(target, path); ok {
			return Node{}, true, e.diagnose(call, "call", fmt.Sprintf("field %s.%s is selected, but the summary of %s gives it no value", target, strings.Join(path, "."), c))
		}
	}
	n := Node{Kind: "assign", Updates: map[string]expr.Expression{}, inMemory: true}
	for _, w := range s.Writes {
		field := target + "." + strings.Join(w.Path, ".")
		symbol, ok := e.opts.Field(target, w.Path)
		if !ok {
			n.Unmodelled = append(n.Unmodelled, field)
			continue
		}
		sym, ok := e.opts.Scope[string(symbol)]
		if !ok {
			return Node{}, true, e.diagnose(call, "assign", "field "+field+" maps to "+string(symbol)+", which is not a model symbol")
		}
		if _, dup := n.Updates[string(symbol)]; dup {
			return Node{}, true, e.diagnose(call, "assign", "aliased field assignment to "+string(symbol))
		}
		bindings := map[string]string{}
		for _, p := range summaries.Placeholders(w.Value) {
			v, ok := e.opts.Field(source, s.Reads[p])
			if !ok {
				return Node{}, true, e.diagnose(call, "field", fmt.Sprintf("unmapped field %s.%s, which the write of %s reads", source, strings.Join(s.Reads[p], "."), field))
			}
			bindings[p] = string(v)
		}
		v, err := summaries.Substitute(w.Value, bindings)
		if err != nil {
			return Node{}, true, err
		}
		if _, err := e.opts.Compiler.Compile(expr.Expression(v), e.opts.Scope, sym.Type); err != nil {
			return Node{}, true, e.diagnose(call, "assign", "write of "+field+": "+err.Error())
		}
		n.Updates[string(symbol)] = expr.Expression(v)
	}
	return n, true, nil
}

// objectArg resolves an object argument, a local or parameter optionally
// behind &, to its name.
func (e *extractor) objectArg(arg ast.Expr) (string, error) {
	x := ast.Unparen(arg)
	if u, ok := x.(*ast.UnaryExpr); ok && u.Op == token.AND {
		x = ast.Unparen(u.X)
	}
	id, ok := x.(*ast.Ident)
	if ok {
		if _, isVar := e.info.Uses[id].(*types.Var); isVar {
			return id.Name, nil
		}
	}
	return "", e.diagnose(arg, "call", "object argument must be a local or parameter")
}

// succeed records that a summarized in-memory call returned no error: the
// error symbol a statement binds it to is false, and so is each outcome
// symbol of that error the frontend declared.
func (e *extractor) succeed(n *Node, at ast.Node, errorTarget string) error {
	if errorTarget == "" {
		return nil
	}
	if sym, ok := e.opts.Scope[errorTarget]; !ok || sym.Type != "bool" || !e.opts.Errors[errorTarget] {
		return e.diagnose(at, "assign", "unknown error target "+errorTarget)
	}
	n.Updates[errorTarget] = "false"
	for _, outcome := range errorOutcomes {
		if _, ok := e.opts.Scope[errorTarget+"_"+outcome]; ok {
			n.Updates[errorTarget+"_"+outcome] = "false"
		}
	}
	return nil
}
