package goflow

import (
	"fmt"
	"generative-architecture/pkg/expr"
	"go/ast"
	"go/constant"
	"go/types"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// ExtractTyped lowers a loaded method using its package's type information.
// Identifiers resolve by symbol rather than by name: locals and parameters map
// to their Scope entry, and constants lower to their literal value. Everything
// else behaves as Extract.
func ExtractTyped(l Loaded, opts Options) (*Program, error) {
	if l.Package == nil || l.Package.TypesInfo == nil || l.Package.Fset == nil {
		return nil, fmt.Errorf("ExtractTyped requires a package with type information")
	}
	if l.Func == nil || l.Func.Body == nil {
		return nil, fmt.Errorf("ExtractTyped requires a method with a body")
	}
	source, err := os.ReadFile(l.Path)
	if err != nil {
		return nil, err
	}
	if opts.File == "" {
		opts.File = l.Path
	}
	e := &extractor{opts: opts, fset: l.Package.Fset, info: l.Package.TypesInfo}
	return e.run(source, l.Func)
}

// Callee identifies a called function or method by symbol: the package that
// declares it, the receiver type name without pointer (empty for functions)
// and the function or method name.
type Callee struct {
	Package  string `yaml:"package,omitempty"`
	Receiver string `yaml:"receiver,omitempty"`
	Method   string `yaml:"method"`
}

// callee resolves the symbol a call invokes. It reports false in text mode and
// for calls that are not to a declared function or method, such as calls of
// function values, conversions and builtins.
func (e *extractor) callee(call *ast.CallExpr) (Callee, bool) {
	if e.info == nil {
		return Callee{}, false
	}
	var obj types.Object
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		obj = e.info.Uses[f]
	case *ast.SelectorExpr:
		if sel, ok := e.info.Selections[f]; ok {
			if sel.Kind() != types.MethodVal {
				return Callee{}, false
			}
			return calleeOf(sel.Obj(), sel.Recv())
		}
		obj = e.info.Uses[f.Sel]
	}
	return calleeOf(obj, nil)
}

// calleeOf builds a Callee for a function object; recv overrides the
// receiver type for a method selected through an interface or embedding.
func calleeOf(obj types.Object, recv types.Type) (Callee, bool) {
	fn, ok := obj.(*types.Func)
	if !ok {
		return Callee{}, false
	}
	c := Callee{Method: fn.Name()}
	if fn.Pkg() != nil {
		c.Package = fn.Pkg().Path()
	}
	if recv == nil && fn.Signature().Recv() != nil {
		recv = fn.Signature().Recv().Type()
	}
	if recv != nil {
		if p, ok := types.Unalias(recv).(*types.Pointer); ok {
			recv = p.Elem()
		}
		if named, ok := types.Unalias(recv).(*types.Named); ok {
			c.Receiver = named.Obj().Name()
		}
	}
	return c, true
}

// declareErrors registers every name an assignment in body binds to a value
// of type error: it joins Errors and is declared as a bool in Scope so that
// err != nil lowers to it. Statements are lowered last-first, so this runs
// before the body is translated. The assignment itself must still be
// summarized for extraction to succeed.
func (e *extractor) declareErrors(body *ast.BlockStmt) {
	ast.Inspect(body, func(n ast.Node) bool {
		x, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range x.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name == "_" {
				continue
			}
			if obj := e.info.ObjectOf(id); obj != nil && isErrorType(obj.Type()) {
				e.opts.Errors[id.Name] = true
				if _, ok := e.opts.Scope[id.Name]; !ok {
					e.opts.Scope[id.Name] = expr.Symbol{Type: "bool", Quint: id.Name}
				}
			}
		}
		return true
	})
}

// bindResults records one Results target per left-hand side of a summarized
// assignment: "" for the blank identifier, otherwise the Scope symbol the name
// maps to (through Targets when present).
func (e *extractor) bindResults(n Node, x *ast.AssignStmt) (Node, error) {
	if len(n.Results) > 0 {
		return n, e.diagnose(x, "assign", "summary may not set results for an assignment")
	}
	for _, lhs := range x.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok {
			return n, e.diagnose(lhs, "assign", "unsupported assignment target")
		}
		if id.Name == "_" {
			n.Results = append(n.Results, "")
			continue
		}
		target := id.Name
		if t, ok := e.opts.Targets[id.Name]; ok {
			target = t
		}
		if _, ok := e.opts.Scope[target]; !ok {
			return n, e.diagnose(lhs, "assign", "unknown assignment target "+target)
		}
		n.Results = append(n.Results, expr.Expression(target))
		if obj := e.info.ObjectOf(id); obj != nil && isErrorType(obj.Type()) {
			n.Error = expr.Expression(target)
		}
	}
	return n, nil
}

// isErrorType reports whether t is the predeclared error interface.
func isErrorType(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}

// selector lowers a selector expression through type information. A field
// selection rooted at a local or parameter becomes the JSON path of its
// struct tags and is mapped to a model expression by Options.Field; an
// unmapped field is a diagnostic. A package-qualified constant lowers to its
// literal. Other selectors fall back to name-based lowering, as does any
// field selection when no Field callback is configured.
func (e *extractor) selector(x *ast.SelectorExpr) (expr.Expression, bool, error) {
	if c, ok := e.info.Uses[x.Sel].(*types.Const); ok {
		if _, isPkg := e.info.Uses[rootIdent(x.X)].(*types.PkgName); isPkg {
			v, err := e.constant(x, c)
			return v, true, err
		}
	}
	if e.opts.Field == nil {
		return "", false, nil
	}
	sel, ok := e.info.Selections[x]
	if !ok || sel.Kind() != types.FieldVal {
		return "", false, nil
	}
	root, path, err := e.fieldPath(x)
	if err != nil {
		return "", true, err
	}
	if v, ok := e.opts.Field(root, path); ok {
		return v, true, nil
	}
	return "", true, e.diagnose(x, "field", "unmapped field "+root+"."+strings.Join(path, "."))
}

// fieldPath walks a chain of field selections down to its root identifier,
// returning the root's name and the JSON path of every hop, including
// promoted fields of inlined embedded structs.
func (e *extractor) fieldPath(x *ast.SelectorExpr) (string, []string, error) {
	var hops []*ast.SelectorExpr
	var n ast.Expr = x
	for {
		s, ok := ast.Unparen(n).(*ast.SelectorExpr)
		if !ok {
			break
		}
		sel, ok := e.info.Selections[s]
		if !ok || sel.Kind() != types.FieldVal {
			return "", nil, e.diagnose(s, "field", "unsupported selector")
		}
		hops = append(hops, s)
		n = s.X
	}
	root, ok := ast.Unparen(n).(*ast.Ident)
	if !ok {
		return "", nil, e.diagnose(n, "field", "field selection must be rooted at an identifier")
	}
	if _, ok := e.info.Uses[root].(*types.Var); !ok {
		return "", nil, e.diagnose(root, "field", "field selection must be rooted at a variable")
	}
	var path []string
	for i := len(hops) - 1; i >= 0; i-- {
		segment, err := jsonPath(e.info.Selections[hops[i]])
		if err != nil {
			return "", nil, e.diagnose(hops[i], "field", err.Error())
		}
		path = append(path, segment...)
	}
	return root.Name, path, nil
}

// jsonPath returns the JSON names along a field selection, following the
// same tag rules as encoding/json: the tag name, else the Go field name;
// inlined embedded structs contribute nothing; "-" and unexported fields are
// not serialized.
func jsonPath(sel *types.Selection) ([]string, error) {
	t := sel.Recv()
	var path []string
	for _, i := range sel.Index() {
		st, ok := deref(t).Underlying().(*types.Struct)
		if !ok {
			return nil, fmt.Errorf("field selection traverses a non-struct")
		}
		f := st.Field(i)
		if !f.Exported() {
			return nil, fmt.Errorf("unexported field %s is not serialized", f.Name())
		}
		tag := strings.Split(reflect.StructTag(st.Tag(i)).Get("json"), ",")
		name := tag[0]
		switch {
		case name == "-":
			return nil, fmt.Errorf("field %s is not serialized", f.Name())
		case name == "" && (f.Embedded() || slices.Contains(tag[1:], "inline")):
		case name == "":
			path = append(path, f.Name())
		default:
			path = append(path, name)
		}
		t = f.Type()
	}
	return path, nil
}

func deref(t types.Type) types.Type {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

// rootIdent returns the identifier a selector chain starts from, or nil.
func rootIdent(n ast.Expr) *ast.Ident {
	for {
		switch x := ast.Unparen(n).(type) {
		case *ast.Ident:
			return x
		case *ast.SelectorExpr:
			n = x.X
		default:
			return nil
		}
	}
}

// typedIdent resolves an identifier through type information. It reports
// false when the identifier is not a symbol it understands, in which case
// translate falls back to name-based resolution.
func (e *extractor) typedIdent(x *ast.Ident) (expr.Expression, bool, error) {
	switch o := e.info.Uses[x].(type) {
	case *types.Const:
		v, err := e.constant(x, o)
		return v, true, err
	case *types.Var:
		if o.IsField() {
			return "", false, nil
		}
		if _, ok := e.opts.Scope[o.Name()]; ok {
			return expr.Expression(o.Name()), true, nil
		}
		if o.Pkg() != nil && o.Parent() != o.Pkg().Scope() {
			return "", true, e.diagnose(x, "identifier", "unbound local variable "+o.Name())
		}
	}
	return "", false, nil
}

// constant lowers a constant of basic kind to a literal expression.
func (e *extractor) constant(n ast.Node, c *types.Const) (expr.Expression, error) {
	if _, ok := types.Unalias(c.Type()).Underlying().(*types.Basic); !ok {
		return "", e.diagnose(n, "constant", "unsupported constant type "+c.Type().String())
	}
	v := c.Val()
	switch v.Kind() {
	case constant.Bool:
		return expr.Expression(strconv.FormatBool(constant.BoolVal(v))), nil
	case constant.String:
		return expr.Expression(strconv.Quote(constant.StringVal(v))), nil
	case constant.Int:
		if i, exact := constant.Int64Val(v); exact {
			return expr.Expression(strconv.FormatInt(i, 10)), nil
		}
	}
	return "", e.diagnose(n, "constant", "unsupported constant value "+v.ExactString())
}
