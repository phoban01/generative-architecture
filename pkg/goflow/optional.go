package goflow

import (
	"go/ast"
	"go/token"
	"go/types"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// A field of pointer-to-basic type, such as a *bool or an *int64 in an API
// type, is an optional value. The model holds it as its basic type and
// reads nil as the zero value of that type. So *p lowers to the field's
// value, p != nil to a test of the value against its zero, and p == nil to
// the opposite test.

// optionalField reports whether x selects a field of pointer-to-basic type
// that Options.Field maps, and returns the field's model expression.
func (e *extractor) optionalField(x ast.Expr) (expr.Expression, bool, error) {
	sel, ok := ast.Unparen(x).(*ast.SelectorExpr)
	if !ok || e.opts.Field == nil {
		return "", false, nil
	}
	s, ok := e.info.Selections[sel]
	if !ok || s.Kind() != types.FieldVal {
		return "", false, nil
	}
	p, ok := types.Unalias(s.Type()).(*types.Pointer)
	if !ok {
		return "", false, nil
	}
	if _, basic := types.Unalias(p.Elem()).Underlying().(*types.Basic); !basic {
		return "", false, nil
	}
	v, ok, err := e.selector(sel)
	return v, ok, err
}

// deref lowers *p for an optional field to the field's value.
func (e *extractor) deref(x *ast.StarExpr) (expr.Expression, bool, error) {
	return e.optionalField(x.X)
}

// nilTest lowers p != nil and p == nil for an optional field: the field is
// set when its value is not the zero value of its model type.
func (e *extractor) nilTest(x *ast.BinaryExpr) (expr.Expression, bool, error) {
	v, ok, err := e.optionalField(x.X)
	if !ok || err != nil {
		return "", ok, err
	}
	s, err := e.opts.Compiler.Compile(v, e.opts.Scope, "")
	if err != nil {
		return "", true, e.unsupported(x.X, err)
	}
	zero := map[string]string{"bool": "false", "int": "0", "string": `""`}[s.Type]
	if zero == "" {
		return "", true, e.diagnose(x, "expression", "optional field of type "+s.Type+" has no zero value")
	}
	op := "!="
	if x.Op == token.EQL {
		op = "=="
	}
	return expr.Expression("(" + string(v) + " " + op + " " + zero + ")"), true, nil
}
