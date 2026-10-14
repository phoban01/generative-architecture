package watchgen

import (
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/goflow"
	"go/ast"
	"go/token"
	"strconv"
)

// A setup preamble may construct unstructured objects and set each exact GVK.
// Other statements remain unsupported: arbitrary setup control flow is not erased.
func unstructuredObjects(fs *token.FileSet, stmts []ast.Stmt, qualified func(ast.Expr) string, bindings map[string]effect.Resource) (map[string]effect.Resource, error) {
	out := map[string]effect.Resource{}
	if len(stmts)%2 != 0 {
		return nil, diag(fs, stmts[len(stmts)-1], "preamble", "unsupported setup preamble")
	}
	for i := 0; i < len(stmts); i += 2 {
		assign, ok := stmts[i].(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return nil, diag(fs, stmts[i], "preamble", "expected an unstructured object declaration")
		}
		name, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || name.Name == "_" {
			return nil, diag(fs, assign.Lhs[0], "watch-object", "invalid watch object name")
		}
		if _, ok := out[name.Name]; ok {
			return nil, diag(fs, name, "watch-object", "duplicate watch object "+name.Name)
		}
		ptr, ok := assign.Rhs[0].(*ast.UnaryExpr)
		if !ok || ptr.Op != token.AND {
			return nil, diag(fs, assign.Rhs[0], "watch-object", "expected a pointer to an unstructured object")
		}
		lit, ok := ptr.X.(*ast.CompositeLit)
		if !ok || qualified(lit.Type) != "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured.Unstructured" || len(lit.Elts) != 0 {
			return nil, diag(fs, ptr.X, "watch-object", "unsupported watch object initializer")
		}
		stmt, ok := stmts[i+1].(*ast.ExprStmt)
		if !ok {
			return nil, diag(fs, stmts[i+1], "SetGroupVersionKind", "expected a SetGroupVersionKind call")
		}
		call, ok := stmt.X.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 || call.Ellipsis.IsValid() {
			return nil, diag(fs, stmt.X, "SetGroupVersionKind", "expected exactly one GVK argument")
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || goflow.Code(sel.X) != name.Name || sel.Sel.Name != "SetGroupVersionKind" {
			return nil, diag(fs, call.Fun, "SetGroupVersionKind", "expected "+name.Name+".SetGroupVersionKind")
		}
		r, ok := bindings[goflow.Code(call.Args[0])]
		if !ok {
			value, ok := call.Args[0].(*ast.CompositeLit)
			if !ok || qualified(value.Type) != "k8s.io/apimachinery/pkg/runtime/schema.GroupVersionKind" {
				return nil, diag(fs, call.Args[0], "gvk", "unresolved dynamic watch GVK: supply a bounded GVK binding")
			}
			seen := map[string]bool{}
			for _, element := range value.Elts {
				kv, ok := element.(*ast.KeyValueExpr)
				if !ok {
					return nil, diag(fs, element, "gvk", "GVK requires named literal fields")
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || seen[key.Name] {
					return nil, diag(fs, kv.Key, "gvk", "invalid or duplicate GVK field")
				}
				seen[key.Name] = true
				str, ok := kv.Value.(*ast.BasicLit)
				if !ok || str.Kind != token.STRING {
					return nil, diag(fs, kv.Value, "gvk", "GVK requires constant strings or a bounded binding")
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
					return nil, diag(fs, key, "gvk", "unknown GVK field "+key.Name)
				}
			}
		}
		if r.Version == "" || r.Kind == "" {
			return nil, diag(fs, call, "gvk", "unstructured watch requires a complete GVK")
		}
		out[name.Name] = r
	}
	return out, nil
}
