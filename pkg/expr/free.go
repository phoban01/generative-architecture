package expr

import (
	"sort"

	"cel.dev/cel-go/cel"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

// FreeIdentifiers lists the identifiers a CEL expression reads that no
// comprehension binds: the state a guard or property depends on. A dotted
// reference such as port.ready counts as its root, port. The name of a
// called function is not an identifier. The list is sorted
// and has no duplicates. A parse error is returned as is.
func FreeIdentifiers(source Expression) ([]string, error) {
	env, err := cel.NewEnv()
	if err != nil {
		return nil, err
	}
	a, issues := env.Parse(string(source))
	if issues.Err() != nil {
		return nil, issues.Err()
	}
	free := map[string]bool{}
	var visit func(e *exprpb.Expr, bound map[string]bool)
	visit = func(e *exprpb.Expr, bound map[string]bool) {
		if e == nil {
			return
		}
		if i := e.GetIdentExpr(); i != nil {
			if !bound[i.Name] {
				free[i.Name] = true
			}
			return
		}
		if s := e.GetSelectExpr(); s != nil {
			visit(s.Operand, bound)
			return
		}
		if c := e.GetCallExpr(); c != nil {
			visit(c.Target, bound)
			for _, v := range c.Args {
				visit(v, bound)
			}
			return
		}
		if s := e.GetStructExpr(); s != nil {
			for _, v := range s.Entries {
				visit(v.GetMapKey(), bound)
				visit(v.Value, bound)
			}
			return
		}
		if l := e.GetListExpr(); l != nil {
			for _, v := range l.Elements {
				visit(v, bound)
			}
			return
		}
		if c := e.GetComprehensionExpr(); c != nil {
			// The range and the initial accumulator are evaluated outside
			// the binding; the condition, the step and the result inside it.
			visit(c.IterRange, bound)
			visit(c.AccuInit, bound)
			inner := map[string]bool{c.IterVar: true, c.AccuVar: true}
			for k := range bound {
				inner[k] = true
			}
			visit(c.LoopCondition, inner)
			visit(c.LoopStep, inner)
			visit(c.Result, inner)
		}
	}
	visit(a.Expr(), map[string]bool{})
	out := make([]string, 0, len(free))
	for name := range free {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
