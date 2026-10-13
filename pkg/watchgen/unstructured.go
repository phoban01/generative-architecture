package watchgen

import (
	"fmt"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/goflow"
	"go/ast"
	"go/token"
	"strconv"
)

// A setup preamble may construct unstructured objects and set each exact GVK.
// Other statements remain unsupported: arbitrary setup control flow is not erased.
func unstructuredObjects(stmts []ast.Stmt, qualified func(ast.Expr) string, bindings map[string]effect.Resource) (map[string]effect.Resource, error) {
	out := map[string]effect.Resource{}
	if len(stmts)%2 != 0 {
		return nil, fmt.Errorf("unsupported setup preamble")
	}
	for i := 0; i < len(stmts); i += 2 {
		assign, ok := stmts[i].(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return nil, fmt.Errorf("expected unstructured object declaration")
		}
		name, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || name.Name == "_" {
			return nil, fmt.Errorf("invalid watch object name")
		}
		if _, ok := out[name.Name]; ok {
			return nil, fmt.Errorf("duplicate watch object")
		}
		ptr, ok := assign.Rhs[0].(*ast.UnaryExpr)
		if !ok || ptr.Op != token.AND {
			return nil, fmt.Errorf("expected unstructured object pointer")
		}
		lit, ok := ptr.X.(*ast.CompositeLit)
		if !ok || qualified(lit.Type) != "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured.Unstructured" || len(lit.Elts) != 0 {
			return nil, fmt.Errorf("unsupported watch object initializer")
		}
		stmt, ok := stmts[i+1].(*ast.ExprStmt)
		if !ok {
			return nil, fmt.Errorf("expected SetGroupVersionKind")
		}
		call, ok := stmt.X.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 || call.Ellipsis.IsValid() {
			return nil, fmt.Errorf("expected one GVK")
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || goflow.Code(sel.X) != name.Name || sel.Sel.Name != "SetGroupVersionKind" {
			return nil, fmt.Errorf("expected watch object SetGroupVersionKind")
		}
		r, ok := bindings[goflow.Code(call.Args[0])]
		if !ok {
			value, ok := call.Args[0].(*ast.CompositeLit)
			if !ok || qualified(value.Type) != "k8s.io/apimachinery/pkg/runtime/schema.GroupVersionKind" {
				return nil, fmt.Errorf("unresolved dynamic watch GVK %s: supply a bounded GVK binding", goflow.Code(call.Args[0]))
			}
			seen := map[string]bool{}
			for _, element := range value.Elts {
				kv, ok := element.(*ast.KeyValueExpr)
				if !ok {
					return nil, fmt.Errorf("GVK requires named literal fields")
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || seen[key.Name] {
					return nil, fmt.Errorf("invalid GVK field")
				}
				seen[key.Name] = true
				str, ok := kv.Value.(*ast.BasicLit)
				if !ok || str.Kind != token.STRING {
					return nil, fmt.Errorf("GVK requires constant strings or a bounded binding")
				}
				v, err := strconv.Unquote(str.Value)
				if err != nil {
					return nil, err
				}
				switch key.Name {
				case "Group":
					r.Group = v
				case "Version":
					r.Version = v
				case "Kind":
					r.Kind = v
				default:
					return nil, fmt.Errorf("unknown GVK field")
				}
			}
		}
		if r.Version == "" || r.Kind == "" {
			return nil, fmt.Errorf("unstructured watch requires complete GVK")
		}
		out[name.Name] = r
	}
	return out, nil
}
