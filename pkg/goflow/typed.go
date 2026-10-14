package goflow

import (
	"fmt"
	"generative-architecture/pkg/expr"
	"go/ast"
	"go/constant"
	"go/types"
	"os"
	"strconv"
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
