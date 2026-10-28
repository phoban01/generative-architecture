package goflow

import (
	"go/ast"
	"go/constant"
	"go/types"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

// requeue reads a reconcile result literal that asks for a requeue, such
// as reconcile.Result{RequeueAfter: time.Minute}. The literal must be a
// struct with a Requeue and a RequeueAfter field and set only those, by
// key. Requeue must be a constant. RequeueAfter may be any expression and
// is taken to be positive unless it is the constant zero; a positive delay
// wins over Requeue, as in controller-runtime. It returns the requeue
// (ir.RequeueImmediate, ir.RequeueAfter or empty) and the source of the
// delay.
func (e *extractor) requeue(result ast.Expr) (string, string, error) {
	x := ast.Unparen(result)
	lit, ok := x.(*ast.CompositeLit)
	if !ok || !resultType(e.info.TypeOf(lit)) {
		return "", "", e.diagnose(result, "return", "result must be the zero value of its type or a reconcile result literal")
	}
	var requeue bool
	var after ast.Expr
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			return "", "", e.diagnose(el, "return", "reconcile result fields must be set by name")
		}
		key, _ := kv.Key.(*ast.Ident)
		if key == nil {
			return "", "", e.diagnose(kv.Key, "return", "reconcile result fields must be set by name")
		}
		tv := e.info.Types[kv.Value]
		switch key.Name {
		case "Requeue":
			if tv.Value == nil || tv.Value.Kind() != constant.Bool {
				return "", "", e.diagnose(kv.Value, "return", "Requeue must be a constant")
			}
			requeue = constant.BoolVal(tv.Value)
		case "RequeueAfter":
			if tv.Value != nil && constant.Sign(tv.Value) == 0 {
				continue
			}
			if tv.Value != nil && constant.Sign(tv.Value) < 0 {
				return "", "", e.diagnose(kv.Value, "return", "RequeueAfter must not be negative")
			}
			after = kv.Value
		default:
			return "", "", e.diagnose(kv, "return", "unsupported reconcile result field "+key.Name)
		}
	}
	switch {
	case after != nil:
		return ir.RequeueAfter, Code(after), nil
	case requeue:
		return ir.RequeueImmediate, "", nil
	}
	return "", "", nil
}

// resultType reports whether t is a struct, or pointer to one, with a bool
// field Requeue and an integer field RequeueAfter: the shape of
// controller-runtime's reconcile.Result.
func resultType(t types.Type) bool {
	if t == nil {
		return false
	}
	st, ok := deref(t).Underlying().(*types.Struct)
	if !ok {
		return false
	}
	var requeue, after bool
	for i := range st.NumFields() {
		f := st.Field(i)
		b, ok := f.Type().Underlying().(*types.Basic)
		if !ok {
			continue
		}
		switch {
		case f.Name() == "Requeue" && b.Kind() == types.Bool:
			requeue = true
		case f.Name() == "RequeueAfter" && b.Info()&types.IsInteger != 0:
			after = true
		}
	}
	return requeue && after
}
