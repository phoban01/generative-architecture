package watchgen

import (
	"fmt"
	"generative-architecture/pkg/goflow"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// Recognize the common type-assert, reject-empty-reference, return-one-key mapper.
// The selected Name field is extracted, never pinned to an expected business field.
// Other control flow and namespace transformations need another frontend pattern.
func extractMapping(f *ast.File, imports map[string]string, rule *Rule) error {
	fn := goflow.FindMethod(f, "", rule.Mapping)
	if fn == nil || fn.Body == nil || len(fn.Body.List) != 3 || len(fn.Type.Params.List) != 2 {
		return fmt.Errorf("unsupported mapping function %s", rule.Mapping)
	}
	alias := func(path string) string {
		for name, p := range imports {
			if p == path {
				return name
			}
		}
		return ""
	}
	contextAlias, clientAlias, reconcileAlias := alias("context"), alias("sigs.k8s.io/controller-runtime/pkg/client"), alias("sigs.k8s.io/controller-runtime/pkg/reconcile")
	if contextAlias == "" || clientAlias == "" || reconcileAlias == "" {
		return fmt.Errorf("unresolved mapper imports")
	}
	for _, p := range fn.Type.Params.List {
		if len(p.Names) != 1 {
			return fmt.Errorf("unsupported mapper parameters")
		}
	}
	ctxName, objName := fn.Type.Params.List[0].Names[0].Name, fn.Type.Params.List[1].Names[0].Name
	assign, ok := fn.Body.List[0].(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return fmt.Errorf("mapper requires a type assertion")
	}
	local, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return fmt.Errorf("unsupported mapper local")
	}
	valid, ok := assign.Lhs[1].(*ast.Ident)
	if !ok {
		return fmt.Errorf("unsupported mapper assertion")
	}
	assertion, ok := assign.Rhs[0].(*ast.TypeAssertExpr)
	if !ok {
		return fmt.Errorf("unsupported mapper assertion")
	}
	pointer, ok := assertion.Type.(*ast.StarExpr)
	if !ok {
		return fmt.Errorf("mapper assertion needs object pointer")
	}
	typ, ok := pointer.X.(*ast.SelectorExpr)
	if !ok {
		return fmt.Errorf("mapper type must be selected")
	}
	pkg, ok := typ.X.(*ast.Ident)
	if !ok || imports[pkg.Name]+"."+typ.Sel.Name != rule.GoType {
		return fmt.Errorf("mapper type differs from watched type")
	}
	ret, ok := fn.Body.List[2].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return fmt.Errorf("unsupported mapper return")
	}
	var path string
	ast.Inspect(ret, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		id, ok := kv.Key.(*ast.Ident)
		if ok && id.Name == "Name" {
			path = goflow.Code(kv.Value)
		}
		return true
	})
	parts := strings.Split(path, ".")
	if len(parts) < 2 || parts[0] != local.Name {
		return fmt.Errorf("mapper request name must be an object field path")
	}
	for _, part := range parts {
		if !token.IsIdentifier(part) {
			return fmt.Errorf("unsupported mapper name expression")
		}
	}
	expected := fmt.Sprintf(`package p
func %s(%s %s.Context,%s %s.Object)[]%s.Request{
 %s,%s:=%s.(*%s.%s)
 if !%s||%s==""{return nil}
 return []%s.Request{{NamespacedName:%s.ObjectKey{Namespace:%s.Namespace,Name:%s}}}
}`, fn.Name.Name, ctxName, contextAlias, objName, clientAlias, reconcileAlias, local.Name, valid.Name, objName, pkg.Name, typ.Sel.Name, valid.Name, path, reconcileAlias, clientAlias, local.Name, path)
	parsed, err := parser.ParseFile(token.NewFileSet(), "expected.go", expected, 0)
	if err != nil {
		return err
	}
	normalized := *fn
	normalized.Doc = nil
	if goflow.Code(parsed.Decls[0]) != goflow.Code(&normalized) {
		return fmt.Errorf("unsupported mapping control flow in %s", fn.Name.Name)
	}
	rule.NamePath = parts[1:]
	rule.MappingSHA256 = goflow.Hash([]byte(goflow.Code(fn)))
	return nil
}
