package goflow

import (
	"go/ast"
	"go/constant"
	"go/token"
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
//
// The result may also be a local that holds such a literal (see
// resultLocals); the literal it was defined from is read in its place.
func (e *extractor) requeue(result ast.Expr) (string, string, error) {
	x := ast.Unparen(result)
	if id, ok := x.(*ast.Ident); ok {
		if lit, ok := e.results[e.info.ObjectOf(id)]; ok {
			x = lit
		}
	}
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

// resultLocals finds the locals of body that hold a reconcile result
// literal for good: each is defined once by x := T{...} with T of
// reconcile result type, and nothing assigns it, a field of it or an
// element of it again, or takes its address. Such a local, such as
// scheduledResult := ctrl.Result{RequeueAfter: d}, is returned as if the
// literal stood in its place.
func (e *extractor) resultLocals(body *ast.BlockStmt) map[types.Object]*ast.CompositeLit {
	defined := map[types.Object]*ast.CompositeLit{}
	writes := map[types.Object]int{}
	write := func(x ast.Expr) {
		if id := rootIdent(x); id != nil {
			if obj := e.info.ObjectOf(id); obj != nil {
				writes[obj]++
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range x.Lhs {
				write(lhs)
			}
			if x.Tok != token.DEFINE || len(x.Lhs) != 1 || len(x.Rhs) != 1 {
				return true
			}
			id, ok := x.Lhs[0].(*ast.Ident)
			lit, isLit := ast.Unparen(x.Rhs[0]).(*ast.CompositeLit)
			if !ok || !isLit || !resultType(e.info.TypeOf(lit)) {
				return true
			}
			if _, isPtr := types.Unalias(e.info.TypeOf(lit)).(*types.Pointer); isPtr {
				return true
			}
			if obj := e.info.Defs[id]; obj != nil {
				defined[obj] = lit
			}
		case *ast.IncDecStmt:
			write(x.X)
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				write(x.X)
			}
		case *ast.ValueSpec:
			for _, name := range x.Names {
				write(name)
			}
		case *ast.SelectorExpr:
			// A pointer method called on the value takes its address.
			if sel, ok := e.info.Selections[x]; ok && sel.Kind() == types.MethodVal {
				recv := sel.Obj().(*types.Func).Signature().Recv()
				_, ptrRecv := types.Unalias(recv.Type()).(*types.Pointer)
				_, ptrValue := types.Unalias(e.info.TypeOf(x.X)).(*types.Pointer)
				if ptrRecv && !ptrValue {
					write(x.X)
				}
			}
		}
		return true
	})
	out := map[types.Object]*ast.CompositeLit{}
	for obj, lit := range defined {
		if writes[obj] == 1 {
			out[obj] = lit
		}
	}
	return out
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
