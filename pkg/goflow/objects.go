package goflow

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// ReturnedError is the error symbol a call in the error position of a
// return statement is bound to, as if the method had assigned the call's
// error to err and returned err.
const ReturnedError = "err"

// summarizeCall offers a call to a resolved symbol to the declared external
// effects, then to Options.Call and last to the built-in object summaries.
// An object summary yields a node of kind assign (see objectCall).
func (e *extractor) summarizeCall(c Callee, call *ast.CallExpr) (Node, bool, error) {
	if n, ok := e.externalCall(c); ok {
		return n, true, nil
	}
	if e.opts.Call != nil {
		n, ok, err := e.opts.Call(c, call, e.expression)
		if ok || err != nil {
			return n, ok, err
		}
	}
	return e.objectCall(c, call)
}

// typedStatement lowers the statements that need type information to
// recognise: declarations of zero-valued object locals, assignments to
// fields of object locals and returns whose error is a summarized call. It
// reports false for any other statement.
func (e *extractor) typedStatement(s ast.Stmt, next int) (int, bool, error) {
	switch x := s.(type) {
	case *ast.AssignStmt:
		if len(x.Lhs) != 1 || len(x.Rhs) != 1 {
			return 0, false, nil
		}
		if x.Tok == token.DEFINE && e.zeroObject(x.Lhs[0], x.Rhs[0]) {
			// Allocating a zero-valued object has no model effect. Its
			// fields enter the model only through summarized calls and
			// field assignments; the caller resets them at the start of
			// each invocation, which is equivalent because control flow is
			// loop-free and the local is not visible before this point.
			return next, true, nil
		}
		if x.Tok == token.ASSIGN && e.opts.Field != nil {
			if sel, ok := ast.Unparen(x.Lhs[0]).(*ast.SelectorExpr); ok {
				if s, ok := e.info.Selections[sel]; ok && s.Kind() == types.FieldVal {
					id, err := e.fieldAssign(x, sel, next)
					return id, true, err
				}
			}
		}
	case *ast.ReturnStmt:
		return e.returnCall(x, next)
	}
	return 0, false, nil
}

// zeroComposite reports whether x is an empty composite literal, optionally
// behind &: the zero value of a struct type, freshly allocated.
func zeroComposite(x ast.Expr) bool {
	x = ast.Unparen(x)
	if u, ok := x.(*ast.UnaryExpr); ok && u.Op == token.AND {
		x = ast.Unparen(u.X)
	}
	c, ok := x.(*ast.CompositeLit)
	return ok && len(c.Elts) == 0
}

// zeroObject reports whether lhs := rhs declares a local holding a zero
// struct value (or a pointer to one) that is not itself a model symbol.
func (e *extractor) zeroObject(lhs, rhs ast.Expr) bool {
	id, ok := lhs.(*ast.Ident)
	if !ok || id.Name == "_" || !zeroComposite(rhs) {
		return false
	}
	if _, ok := e.opts.Scope[id.Name]; ok {
		return false
	}
	obj := e.info.ObjectOf(id)
	if obj == nil {
		return false
	}
	_, isStruct := deref(obj.Type()).Underlying().(*types.Struct)
	return isStruct
}

// mapEntry lowers an index of a map with string keys by a constant key, such
// as cm.Data["value"], when the map is a field selection: the key extends
// the JSON path passed to Options.Field. It reports false when the index is
// of another shape or the callback does not map the path, leaving the
// expression to the generic diagnostic.
func (e *extractor) mapEntry(x *ast.IndexExpr) (expr.Expression, bool) {
	if e.opts.Field == nil {
		return "", false
	}
	key, ok := e.constantKey(x.Index)
	if !ok || !e.stringMap(x.X) {
		return "", false
	}
	sel, ok := ast.Unparen(x.X).(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	root, path, err := e.fieldPath(sel)
	if err != nil {
		return "", false
	}
	return e.opts.Field(root, append(path, key))
}

// constantKey returns the value of a constant string expression.
func (e *extractor) constantKey(x ast.Expr) (string, bool) {
	tv, ok := e.info.Types[x]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	key := constant.StringVal(tv.Value)
	return key, identifierPath(key)
}

// identifierPath reports whether a map key can be a JSON path element of a
// model variable name.
func identifierPath(key string) bool {
	if key == "" {
		return false
	}
	return !strings.ContainsAny(key, ". /")
}

// stringMap reports whether x is a map with string keys.
func (e *extractor) stringMap(x ast.Expr) bool {
	t := e.info.TypeOf(x)
	if t == nil {
		return false
	}
	m, ok := t.Underlying().(*types.Map)
	if !ok {
		return false
	}
	b, ok := m.Key().Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

// absentValues are the values a selected map key's symbol takes when the
// key is absent: the zero value of its model type, as reading a missing
// key in Go returns.
var absentValues = map[string]expr.Expression{"string": `""`, "int": "0", "bool": "false"}

// clear sets the symbol of an omitted selected key to its absent value.
func (e *extractor) clear(at ast.Node, root string, path []string, updates map[string]expr.Expression) error {
	field := root + "." + strings.Join(path, ".")
	target, ok := e.opts.Field(root, path)
	if !ok {
		return e.diagnose(at, "field", "Options.MapKeys names "+field+", which Options.Field does not map")
	}
	symbol, ok := e.opts.Scope[string(target)]
	if !ok {
		return e.diagnose(at, "assign", "field "+field+" maps to "+string(target)+", which is not a model symbol")
	}
	if _, dup := updates[string(target)]; dup {
		return e.diagnose(at, "assign", "aliased field assignment to "+string(target))
	}
	absent, ok := absentValues[symbol.Type]
	if !ok {
		return e.diagnose(at, "assign", "field "+field+" has type "+symbol.Type+", which has no absent value")
	}
	updates[string(target)] = absent
	return nil
}

// fieldAssign lowers an assignment to a field of a local object, mapping the
// field's JSON path through Options.Field to a model symbol. Assigning a map
// literal with constant string keys replaces the whole map: every listed
// key's symbol takes its value, and every key Options.MapKeys selects but
// the literal omits takes its absent value. Keys outside the selection are
// not modelled. Without MapKeys only the listed keys are updated.
func (e *extractor) fieldAssign(x *ast.AssignStmt, lhs *ast.SelectorExpr, next int) (int, error) {
	root, path, err := e.fieldPath(lhs)
	if err != nil {
		return 0, err
	}
	updates := map[string]expr.Expression{}
	set := func(at ast.Node, path []string, value ast.Expr) error {
		target, ok := e.opts.Field(root, path)
		if !ok {
			return e.diagnose(at, "field", "unmapped field "+root+"."+strings.Join(path, "."))
		}
		symbol, ok := e.opts.Scope[string(target)]
		if !ok {
			return e.diagnose(at, "assign", "field "+root+"."+strings.Join(path, ".")+" maps to "+string(target)+", which is not a model symbol")
		}
		if _, dup := updates[string(target)]; dup {
			return e.diagnose(at, "assign", "aliased field assignment to "+string(target))
		}
		v, err := e.expression(value, symbol.Type)
		if err != nil {
			return err
		}
		updates[string(target)] = v
		return nil
	}
	if lit, ok := ast.Unparen(x.Rhs[0]).(*ast.CompositeLit); ok && e.stringMap(lit) {
		listed := map[string]bool{}
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				return 0, e.diagnose(el, "assign", "unsupported map literal element")
			}
			key, ok := e.constantKey(kv.Key)
			if !ok {
				return 0, e.diagnose(kv.Key, "assign", "map literal key must be a constant string")
			}
			listed[key] = true
			if err := set(kv, append(append([]string(nil), path...), key), kv.Value); err != nil {
				return 0, err
			}
		}
		var selected []string
		if e.opts.MapKeys != nil {
			selected = slices.Clone(e.opts.MapKeys(root, path))
			slices.Sort(selected)
		}
		for _, key := range selected {
			if listed[key] {
				continue
			}
			if err := e.clear(lit, root, append(append([]string(nil), path...), key), updates); err != nil {
				return 0, err
			}
		}
	} else if opaque, err := e.opaqueLiteral(root, path, x.Rhs[0]); err != nil {
		return 0, err
	} else if opaque {
		return e.add(Node{Kind: "assign", Unmodelled: []string{root + "." + strings.Join(path, ".")}}, x, next, 0), nil
	} else if err := set(lhs, path, x.Rhs[0]); err != nil {
		return 0, err
	}
	return e.add(Node{Kind: "assign", Updates: updates}, x, next, 0), nil
}

// opaqueLiteral reports whether value, assigned to the field at path of
// root, is a slice, array or struct literal (optionally behind &) that
// writes no model state: Options.Field maps neither the field nor any
// field below it (see selectsBelow). Such a write is recorded without a
// value. A literal that holds a call to a declared function or method is a
// diagnostic, because the call would be dropped. It reports false for any
// other value, which is then lowered as an ordinary field assignment.
func (e *extractor) opaqueLiteral(root string, path []string, value ast.Expr) (bool, error) {
	x := ast.Unparen(value)
	if u, ok := x.(*ast.UnaryExpr); ok && u.Op == token.AND {
		x = ast.Unparen(u.X)
	}
	lit, ok := x.(*ast.CompositeLit)
	if !ok {
		return false, nil
	}
	t := e.info.TypeOf(lit)
	if t == nil {
		return false, nil
	}
	switch t.Underlying().(type) {
	case *types.Slice, *types.Array, *types.Struct:
	default:
		return false, nil
	}
	if _, mapped := e.opts.Field(root, path); mapped {
		return false, nil
	}
	if e.selectsBelow(root, path, t, map[types.Type]bool{}) {
		return false, nil
	}
	var err error
	ast.Inspect(lit, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || err != nil {
			return err == nil
		}
		if c, ok := e.callee(call); ok {
			err = e.diagnose(call, "call", "call to "+c.String()+" inside a literal written without a model value")
		}
		return true
	})
	return err == nil, err
}

// selectsBelow reports whether the abstraction may select a field below
// path of root, whose Go type is t: Options.Field maps a JSON path below
// it, or a map with string keys below it has selected keys. List entries
// add no path element, as the abstraction addresses them by scalar paths.
// Without Options.MapKeys the keys of a map cannot be enumerated, so any
// map with string keys counts as selected. A type that contains itself has
// unbounded paths and counts as selected too.
func (e *extractor) selectsBelow(root string, path []string, t types.Type, visiting map[types.Type]bool) bool {
	t = types.Unalias(t)
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		return e.selectsBelow(root, path, u.Elem(), visiting)
	case *types.Slice:
		return e.selectsBelow(root, path, u.Elem(), visiting)
	case *types.Array:
		return e.selectsBelow(root, path, u.Elem(), visiting)
	case *types.Map:
		if b, ok := u.Key().Underlying().(*types.Basic); !ok || b.Info()&types.IsString == 0 {
			return false
		}
		return e.opts.MapKeys == nil || len(e.opts.MapKeys(root, path)) > 0
	case *types.Struct:
		if visiting[t] {
			// A recursive type has unbounded paths, so a field below it may
			// be selected at any depth.
			return true
		}
		visiting[t] = true
		defer delete(visiting, t)
		for i := range u.NumFields() {
			name, serialized := jsonName(u, i)
			if !serialized {
				continue
			}
			child := path
			if name != "" {
				child = append(slices.Clone(path), name)
				if _, ok := e.opts.Field(root, child); ok {
					return true
				}
			}
			if e.selectsBelow(root, child, u.Field(i).Type(), visiting) {
				return true
			}
		}
	}
	return false
}

// returnCall lowers a return statement whose error result is a call to a
// summarized symbol, such as return reconcile.Result{}, r.Client.Update(...):
// the call becomes a summarized node binding its error to ReturnedError,
// followed by the return of ReturnedError. It reports false for any other
// return statement.
func (e *extractor) returnCall(x *ast.ReturnStmt, next int) (int, bool, error) {
	if len(x.Results) != len(e.opts.Returns) {
		return 0, false, nil
	}
	for i, result := range x.Results {
		if !e.opts.Returns[i].Error {
			continue
		}
		call, ok := ast.Unparen(result).(*ast.CallExpr)
		if !ok {
			continue
		}
		c, ok := e.callee(call)
		if !ok {
			continue
		}
		n, ok, err := e.summarizeCall(c, call)
		if err != nil {
			return 0, true, err
		}
		if !ok {
			return 0, true, e.diagnose(call, "call", "unsummarized call to "+c.String()+"; declare it as a client operation or an external effect")
		}
		if len(n.Results) > 0 {
			return 0, true, e.diagnose(call, "return", "summary may not set results for a returned call")
		}
		if sym, exists := e.opts.Scope[ReturnedError]; exists && (!e.opts.Errors[ReturnedError] || sym.Type != "bool") {
			return 0, true, e.diagnose(call, "return", ReturnedError+" is a model symbol that is not an error")
		}
		e.opts.Errors[ReturnedError] = true
		e.opts.Scope[ReturnedError] = expr.Symbol{Type: "bool", Quint: ReturnedError}
		rewritten := *x
		rewritten.Results = append([]ast.Expr(nil), x.Results...)
		rewritten.Results[i] = &ast.Ident{NamePos: result.Pos(), Name: ReturnedError}
		ret, err := e.statement(&rewritten, next)
		if err != nil {
			return 0, true, err
		}
		e.program.Nodes[ret].Source = Code(x)
		if n.inMemory {
			if err := e.succeed(&n, call, ReturnedError); err != nil {
				return 0, true, err
			}
			return e.add(n, call, ret, 0), true, nil
		}
		n.Results = []expr.Expression{ReturnedError}
		n.Error = ReturnedError
		return e.add(n, call, ret, 0), true, nil
	}
	return 0, false, nil
}
