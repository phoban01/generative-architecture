package goflow

import (
	"go/ast"
	"go/constant"

	"k8s.io/apimachinery/pkg/api/resource"
)

// resourcePackage declares the Kubernetes quantity type.
const resourcePackage = "k8s.io/apimachinery/pkg/api/resource"

// quantityConstructor reports whether c builds a quantity from its
// arguments without side effects.
func quantityConstructor(c Callee) bool {
	return c.Package == resourcePackage && c.Receiver == "" && (c.Method == "MustParse" || c.Method == "NewQuantity")
}

// quantity reads a quantity built from constants: resource.MustParse(s)
// with a constant string, or resource.NewQuantity(v, format) with a
// constant integer and format, optionally dereferenced. It returns the
// canonical string the API server stores, which is an opaque constant in
// the model. It reports false for any other expression; a constructor
// with an argument that is not a constant, or does not parse, is a
// diagnostic.
func (e *extractor) quantity(value ast.Expr) (string, bool, error) {
	x := ast.Unparen(value)
	if s, ok := x.(*ast.StarExpr); ok {
		x = ast.Unparen(s.X)
	}
	call, ok := x.(*ast.CallExpr)
	if !ok || e.info == nil {
		return "", false, nil
	}
	c, ok := e.callee(call)
	if !ok || !quantityConstructor(c) {
		return "", false, nil
	}
	switch c.Method {
	case "MustParse":
		s, ok := e.constantString(call.Args[0])
		if !ok {
			return "", true, e.diagnose(call, "quantity", "resource.MustParse needs a constant string")
		}
		q, err := resource.ParseQuantity(s)
		if err != nil {
			return "", true, e.diagnose(call, "quantity", err.Error())
		}
		return q.String(), true, nil
	default:
		v := e.info.Types[call.Args[0]].Value
		format, ok := e.constantString(call.Args[1])
		if v == nil || v.Kind() != constant.Int || !ok {
			return "", true, e.diagnose(call, "quantity", "resource.NewQuantity needs a constant value and format")
		}
		switch resource.Format(format) {
		case resource.DecimalSI, resource.BinarySI, resource.DecimalExponent:
		default:
			return "", true, e.diagnose(call, "quantity", "unknown quantity format "+format)
		}
		i, exact := constant.Int64Val(v)
		if !exact {
			return "", true, e.diagnose(call, "quantity", "quantity value out of range")
		}
		return resource.NewQuantity(i, resource.Format(format)).String(), true, nil
	}
}

// constantString returns the value of a constant string expression.
func (e *extractor) constantString(x ast.Expr) (string, bool) {
	tv, ok := e.info.Types[x]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}
