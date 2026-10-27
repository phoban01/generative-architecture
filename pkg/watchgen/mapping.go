package watchgen

import (
	"fmt"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// keyTypes are the accepted types of the request key literal; client.ObjectKey
// is an alias of types.NamespacedName.
var keyTypes = map[string]bool{"sigs.k8s.io/controller-runtime/pkg/client.ObjectKey": true, "k8s.io/apimachinery/pkg/types.NamespacedName": true}

// Recognize the common type-assert, reject-empty-reference, return-one-key mapper.
// The selected Name field is extracted, never pinned to an expected business field.
// Other control flow and namespace transformations need another frontend pattern.
// imports maps package names to import paths for rebuilding the expected
// source; qualified resolves selected types.
func extractMapping(fs *token.FileSet, f *ast.File, imports map[string]string, qualified func(ast.Expr) string, rule *Rule) error {
	fn := goflow.FindMethod(f, "", rule.Mapping)
	if fn == nil || fn.Body == nil || len(fn.Body.List) != 3 || len(fn.Type.Params.List) != 2 {
		var at ast.Node = f.Name
		if fn != nil {
			at = fn.Name
		}
		return diag(fs, at, "mapper", "unsupported mapping function "+rule.Mapping)
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
		return diag(fs, fn.Name, "mapper", "unresolved mapper imports: context, client and reconcile must be imported")
	}
	for _, p := range fn.Type.Params.List {
		if len(p.Names) != 1 {
			return diag(fs, p, "mapper", "unsupported mapper parameters")
		}
	}
	ctxName, objName := fn.Type.Params.List[0].Names[0].Name, fn.Type.Params.List[1].Names[0].Name
	assign, ok := fn.Body.List[0].(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return diag(fs, fn.Body.List[0], "mapper", "mapper requires a type assertion")
	}
	local, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return diag(fs, assign.Lhs[0], "mapper", "unsupported mapper local")
	}
	valid, ok := assign.Lhs[1].(*ast.Ident)
	if !ok {
		return diag(fs, assign.Lhs[1], "mapper", "unsupported mapper assertion")
	}
	assertion, ok := assign.Rhs[0].(*ast.TypeAssertExpr)
	if !ok {
		return diag(fs, assign.Rhs[0], "mapper", "unsupported mapper assertion")
	}
	pointer, ok := assertion.Type.(*ast.StarExpr)
	if !ok {
		return diag(fs, assertion.Type, "mapper", "mapper assertion needs object pointer")
	}
	typ, ok := pointer.X.(*ast.SelectorExpr)
	if !ok {
		return diag(fs, pointer.X, "mapper", "mapper type must be selected")
	}
	pkg, ok := typ.X.(*ast.Ident)
	if !ok || qualified(typ) != rule.GoType {
		return diag(fs, typ, "mapper", "mapper type differs from watched type")
	}
	ret, ok := fn.Body.List[2].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return diag(fs, fn.Body.List[2], "mapper", "unsupported mapper return")
	}
	var path string
	keyType := clientAlias + ".ObjectKey"
	ast.Inspect(ret, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		id, ok := kv.Key.(*ast.Ident)
		if !ok {
			return true
		}
		switch id.Name {
		case "Name":
			path = goflow.Code(kv.Value)
		case "NamespacedName":
			if lit, ok := kv.Value.(*ast.CompositeLit); ok && keyTypes[qualified(lit.Type)] {
				keyType = goflow.Code(lit.Type)
			}
		}
		return true
	})
	parts := strings.Split(path, ".")
	if len(parts) < 2 || parts[0] != local.Name {
		return diag(fs, ret, "mapper", "mapper request name must be an object field path")
	}
	for _, part := range parts {
		if !token.IsIdentifier(part) {
			return diag(fs, ret, "mapper", "unsupported mapper name expression")
		}
	}
	expected := fmt.Sprintf(`package p
func %s(%s %s.Context,%s %s.Object)[]%s.Request{
 %s,%s:=%s.(*%s.%s)
 if !%s||%s==""{return nil}
 return []%s.Request{{NamespacedName:%s{Namespace:%s.Namespace,Name:%s}}}
}`, fn.Name.Name, ctxName, contextAlias, objName, clientAlias, reconcileAlias, local.Name, valid.Name, objName, pkg.Name, typ.Sel.Name, valid.Name, path, reconcileAlias, keyType, local.Name, path)
	parsed, err := parser.ParseFile(token.NewFileSet(), "expected.go", expected, 0)
	if err != nil {
		return err
	}
	normalized := *fn
	normalized.Doc = nil
	if goflow.Code(parsed.Decls[0]) != goflow.Code(&normalized) {
		return diag(fs, fn.Name, "mapper", "unsupported mapping control flow in "+fn.Name.Name)
	}
	rule.NamePath = parts[1:]
	rule.MappingSHA256 = goflow.Hash([]byte(goflow.Code(fn)))
	return nil
}
