package watchgen

import (
	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"go/ast"
	"go/token"
	"strconv"
)

// A setup preamble may construct unstructured objects and set each exact GVK.
// Other statements remain unsupported: arbitrary setup control flow is not erased.
// Each unsupported statement gets its own diagnostic, and the check goes on
// with the next statement, so the report lists every preamble gap at once.
func unstructuredObjects(fs *token.FileSet, stmts []ast.Stmt, qualified func(ast.Expr) string, bindings map[string]effect.Resource) (map[string]effect.Resource, error) {
	out := map[string]effect.Resource{}
	var ds []*goflow.Diagnostic
	for i := 0; i < len(stmts); {
		if !isObjectDeclaration(stmts[i]) {
			ds = append(ds, goflow.NewDiagnostic(fs, stmts[i], "preamble", "expected an unstructured object declaration"))
			i++
			continue
		}
		if i+1 == len(stmts) {
			ds = append(ds, goflow.NewDiagnostic(fs, stmts[i], "preamble", "unsupported setup preamble"))
			break
		}
		name, r, err := unstructuredObject(fs, stmts[i], stmts[i+1], qualified, bindings)
		i += 2
		if err != nil {
			d, ok := goflow.AsDiagnostic(err)
			if !ok {
				return nil, err
			}
			ds = append(ds, d)
			continue
		}
		if _, ok := out[name.Name]; ok {
			ds = append(ds, goflow.NewDiagnostic(fs, name, "watch-object", "duplicate watch object "+name.Name))
			continue
		}
		out[name.Name] = r
	}
	if err := goflow.JoinDiagnostics(ds); err != nil {
		return nil, err
	}
	return out, nil
}

// isObjectDeclaration reports whether s has the shape x := v, the first
// statement of an unstructured object declaration.
func isObjectDeclaration(s ast.Stmt) bool {
	assign, ok := s.(*ast.AssignStmt)
	return ok && assign.Tok == token.DEFINE && len(assign.Lhs) == 1 && len(assign.Rhs) == 1
}

// unstructuredObject reads one declaration x := &unstructured.Unstructured{}
// and the x.SetGroupVersionKind call that follows it.
func unstructuredObject(fs *token.FileSet, decl, set ast.Stmt, qualified func(ast.Expr) string, bindings map[string]effect.Resource) (*ast.Ident, effect.Resource, error) {
	var r effect.Resource
	assign := decl.(*ast.AssignStmt)
	name, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || name.Name == "_" {
		return nil, r, diag(fs, assign.Lhs[0], "watch-object", "invalid watch object name")
	}
	ptr, ok := assign.Rhs[0].(*ast.UnaryExpr)
	if !ok || ptr.Op != token.AND {
		return nil, r, diag(fs, assign.Rhs[0], "watch-object", "expected a pointer to an unstructured object")
	}
	lit, ok := ptr.X.(*ast.CompositeLit)
	if !ok || qualified(lit.Type) != "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured.Unstructured" || len(lit.Elts) != 0 {
		return nil, r, diag(fs, ptr.X, "watch-object", "unsupported watch object initializer")
	}
	stmt, ok := set.(*ast.ExprStmt)
	if !ok {
		return nil, r, diag(fs, set, "SetGroupVersionKind", "expected a SetGroupVersionKind call")
	}
	call, ok := stmt.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return nil, r, diag(fs, stmt.X, "SetGroupVersionKind", "expected exactly one GVK argument")
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || goflow.Code(sel.X) != name.Name || sel.Sel.Name != "SetGroupVersionKind" {
		return nil, r, diag(fs, call.Fun, "SetGroupVersionKind", "expected "+name.Name+".SetGroupVersionKind")
	}
	r, ok = bindings[goflow.Code(call.Args[0])]
	if !ok {
		value, ok := call.Args[0].(*ast.CompositeLit)
		if !ok || qualified(value.Type) != "k8s.io/apimachinery/pkg/runtime/schema.GroupVersionKind" {
			return nil, r, diag(fs, call.Args[0], "gvk", "unresolved dynamic watch GVK: supply a bounded GVK binding")
		}
		seen := map[string]bool{}
		for _, element := range value.Elts {
			kv, ok := element.(*ast.KeyValueExpr)
			if !ok {
				return nil, r, diag(fs, element, "gvk", "GVK requires named literal fields")
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || seen[key.Name] {
				return nil, r, diag(fs, kv.Key, "gvk", "invalid or duplicate GVK field")
			}
			seen[key.Name] = true
			str, ok := kv.Value.(*ast.BasicLit)
			if !ok || str.Kind != token.STRING {
				return nil, r, diag(fs, kv.Value, "gvk", "GVK requires constant strings or a bounded binding")
			}
			v, err := strconv.Unquote(str.Value)
			if err != nil {
				return nil, r, err
			}
			switch key.Name {
			case "Group":
				r.Group = v
			case "Version":
				r.Version = v
			case "Kind":
				r.Kind = v
			default:
				return nil, r, diag(fs, key, "gvk", "unknown GVK field "+key.Name)
			}
		}
	}
	if r.Version == "" || r.Kind == "" {
		return nil, r, diag(fs, call, "gvk", "unstructured watch requires a complete GVK")
	}
	return name, r, nil
}
