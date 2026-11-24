package goflow

import (
	"go/ast"
	"go/types"
)

// Logging calls have no model effect. The frontend drops a statement that
// only logs: a call to a logr.Logger method, a logger local bound from
// controller-runtime's log.FromContext or from a Logger method, and a
// reassignment such as log = log.WithValues(...).
const (
	logrPackage    = "github.com/go-logr/logr"
	runtimeLogging = "sigs.k8s.io/controller-runtime/pkg/log"
)

// loggingCallee reports whether c is a logr.Logger method or
// controller-runtime's log.FromContext.
func loggingCallee(c Callee) bool {
	if c.Package == logrPackage && c.Receiver == "Logger" {
		return true
	}
	return c.Package == runtimeLogging && c.Receiver == "" && c.Method == "FromContext"
}

// logging reports whether s, whose call resolves to c, only logs. The
// statement must be the call itself or bind its result to one logger
// local that is not a model symbol. A call inside the logging call that is
// not itself a logging call, a builtin or a conversion is a diagnostic,
// because dropping the statement would drop that call too.
func (e *extractor) logging(s ast.Stmt, call *ast.CallExpr, c Callee) (bool, error) {
	if !loggingCallee(c) {
		return false, nil
	}
	if x, ok := s.(*ast.AssignStmt); ok {
		if len(x.Lhs) != 1 {
			return true, e.diagnose(x, "assign", "unsupported assignment of a logging call")
		}
		id, ok := x.Lhs[0].(*ast.Ident)
		if !ok || id.Name == "_" || !e.loggerVar(id) {
			return true, e.diagnose(x.Lhs[0], "assign", "a logging call must bind a logr.Logger local")
		}
		if _, ok := e.opts.Scope[id.Name]; ok {
			return true, e.diagnose(x.Lhs[0], "assign", "logger local "+id.Name+" is a model symbol")
		}
	}
	var err error
	ast.Inspect(call, func(n ast.Node) bool {
		inner, ok := n.(*ast.CallExpr)
		if !ok || err != nil {
			return err == nil
		}
		if ic, ok := e.callee(inner); ok {
			if !loggingCallee(ic) {
				err = e.diagnose(inner, "call", "call to "+ic.String()+" inside a logging call; dropping the log would drop it")
			}
			return true
		}
		if tv, ok := e.info.Types[inner.Fun]; ok && (tv.IsType() || tv.IsBuiltin()) {
			return true
		}
		err = e.diagnose(inner, "call", "call of a function value inside a logging call; dropping the log would drop it")
		return true
	})
	return true, err
}

// loggerVar reports whether id names a variable of type logr.Logger.
func (e *extractor) loggerVar(id *ast.Ident) bool {
	obj, ok := e.info.ObjectOf(id).(*types.Var)
	if !ok {
		return false
	}
	named, ok := types.Unalias(obj.Type()).(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == logrPackage && named.Obj().Name() == "Logger"
}
