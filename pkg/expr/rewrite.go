package expr

import (
	"cel.dev/cel-go/cel"
	"fmt"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

// Rewrite substitutes checked symbol references on the CEL AST, never strings
// inside literals. Replacements are CEL expressions; the receiving model compiler
// validates their types and namespace. Comprehensions are deliberately rejected
// here until capture-avoiding binder substitution is supported.
func Rewrite(source Expression, bindings map[string]Expression) (Expression, error) {
	env, err := cel.NewEnv()
	if err != nil {
		return "", err
	}
	a, issues := env.Parse(string(source))
	if issues.Err() != nil {
		return "", issues.Err()
	}
	var visit func(*exprpb.Expr) error
	visit = func(e *exprpb.Expr) error {
		if e == nil {
			return nil
		}
		if replacement, ok := bindings[dotted(e)]; ok {
			parsed, issues := env.Parse(string(replacement))
			if issues.Err() != nil {
				return issues.Err()
			}
			e.ExprKind = parsed.Expr().ExprKind
			return nil
		}
		if e.GetComprehensionExpr() != nil {
			return fmt.Errorf("composition of CEL comprehensions is not supported")
		}
		if s := e.GetSelectExpr(); s != nil {
			return visit(s.Operand)
		}
		if c := e.GetCallExpr(); c != nil {
			if err := visit(c.Target); err != nil {
				return err
			}
			for _, v := range c.Args {
				if err := visit(v); err != nil {
					return err
				}
			}
		}
		if s := e.GetStructExpr(); s != nil {
			for _, v := range s.Entries {
				if err := visit(v.GetMapKey()); err != nil {
					return err
				}
				if err := visit(v.Value); err != nil {
					return err
				}
			}
		}
		if l := e.GetListExpr(); l != nil {
			for _, v := range l.Elements {
				if err := visit(v); err != nil {
					return err
				}
			}
		}
		return nil
	}
	tree := a.Expr()
	if err = visit(tree); err != nil {
		return "", err
	}
	out, err := cel.AstToString(cel.ParsedExprToAst(&exprpb.ParsedExpr{Expr: tree}))
	return Expression(out), err
}
