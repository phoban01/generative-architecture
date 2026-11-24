package goflow

import (
	"go/ast"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// apiErrorsPackage declares the outcome predicates client code applies to
// errors returned by the API server.
const apiErrorsPackage = "k8s.io/apimachinery/pkg/api/errors"

// clientPackage is controller-runtime's client package, which declares the
// IgnoreNotFound error filter.
const clientPackage = "sigs.k8s.io/controller-runtime/pkg/client"

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
	return expr.Expression(e.outcomeSymbol(id.Name, outcome)), true, nil
}

// errorFilterCallee reports whether c is client.IgnoreNotFound.
func errorFilterCallee(c Callee) bool {
	return c.Package == clientPackage && c.Receiver == "" && c.Method == "IgnoreNotFound"
}

// errorFilter lowers client.IgnoreNotFound(err), where err is a summarized
// error, to the boolean err && !err_notFound: the call returns nil for a
// not-found error and err otherwise. It reports false for any other
// expression; a filter on anything other than a summarized error is a
// diagnostic.
func (e *extractor) errorFilter(x ast.Expr) (expr.Expression, bool, error) {
	call, ok := ast.Unparen(x).(*ast.CallExpr)
	if !ok {
		return "", false, nil
	}
	c, ok := e.callee(call)
	if !ok || !errorFilterCallee(c) {
		return "", false, nil
	}
	if len(call.Args) != 1 {
		return "", true, e.diagnose(call, "error", "unsupported error filter arity")
	}
	id, ok := ast.Unparen(call.Args[0]).(*ast.Ident)
	if !ok || !e.opts.Errors[id.Name] {
		return "", true, e.diagnose(call.Args[0], "error", "error filter on an unsummarized error")
	}
	return expr.Expression(id.Name + " && !" + e.outcomeSymbol(id.Name, "notFound")), true, nil
}

// outcomeSymbol declares the bool symbol <error>_<outcome> in Scope, if it
// is not there yet, and returns its name.
func (e *extractor) outcomeSymbol(errName, outcome string) string {
	name := errName + "_" + outcome
	if _, ok := e.opts.Scope[name]; !ok {
		e.opts.Scope[name] = expr.Symbol{Type: "bool", Quint: name}
	}
	return name
}
