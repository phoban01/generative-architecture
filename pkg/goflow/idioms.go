package goflow

import (
	"generative-architecture/pkg/expr"
	"go/ast"
)

// apiErrorsPackage declares the outcome predicates client code applies to
// errors returned by the API server.
const apiErrorsPackage = "k8s.io/apimachinery/pkg/api/errors"

// errorOutcomes maps an apierrors predicate to the outcome it tests. The
// lowered symbol is <error>_<outcome>, for example err_notFound.
var errorOutcomes = map[string]string{
	"IsNotFound":      "notFound",
	"IsConflict":      "conflict",
	"IsAlreadyExists": "alreadyExists",
	"IsForbidden":     "forbidden",
}

// idiom lowers a call that tests the outcome of a summarized error, such as
// apierrors.IsNotFound(err), to a boolean symbol declared in Scope. It
// reports false for calls that are not outcome predicates; a predicate
// applied to anything other than a summarized error is a diagnostic.
func (e *extractor) idiom(call *ast.CallExpr) (expr.Expression, bool, error) {
	c, ok := e.callee(call)
	if !ok || c.Package != apiErrorsPackage || c.Receiver != "" {
		return "", false, nil
	}
	outcome, ok := errorOutcomes[c.Method]
	if !ok {
		return "", false, nil
	}
	if len(call.Args) != 1 {
		return "", true, e.diagnose(call, "error", "unsupported error test arity")
	}
	id, ok := ast.Unparen(call.Args[0]).(*ast.Ident)
	if !ok || !e.opts.Errors[id.Name] {
		return "", true, e.diagnose(call.Args[0], "error", "outcome test on an unsummarized error")
	}
	name := id.Name + "_" + outcome
	if _, ok := e.opts.Scope[name]; !ok {
		e.opts.Scope[name] = expr.Symbol{Type: "bool", Quint: name}
	}
	return expr.Expression(name), true, nil
}
