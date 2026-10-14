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
