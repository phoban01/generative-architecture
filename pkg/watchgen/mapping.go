package watchgen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/goflow"
)

// keyTypes are the accepted types of the request key literal; client.ObjectKey
// is an alias of types.NamespacedName.
var keyTypes = map[string]bool{"sigs.k8s.io/controller-runtime/pkg/client.ObjectKey": true, "k8s.io/apimachinery/pkg/types.NamespacedName": true}

// mapper reads one map function: a package function or a method of the
// reconciler, declared in the file of the builder setup.
type mapper struct {
	fs *token.FileSet
	f  *ast.File
	fn *ast.FuncDecl
	// imports maps package names to import paths for rebuilding the
	// expected source; qualified resolves selected types; constant
	// resolves a string constant expression.
	imports   map[string]string
	qualified func(ast.Expr) string
	constant  func(ast.Expr) (string, bool)
	resources map[string]effect.Resource
	// aliases of the context, client and reconcile packages and the names
	// of the context and object parameters.
	ctxAlias, clientAlias, reconcileAlias string
	ctxName, objName                      string
}

// findMethod returns the method name of the receiver type recv declared in f.
func findMethod(f *ast.File, recv ast.Expr, name string) *ast.FuncDecl {
	want := goflow.Code(recv)
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if ok && fn.Name.Name == name && fn.Recv != nil && len(fn.Recv.List) == 1 && goflow.Code(fn.Recv.List[0].Type) == want {
			return fn
		}
	}
	return nil
}

// extract summarizes the map function as a key mapping of rule. It
// recognizes two shapes. A field mapper type-asserts the object, rejects an
// empty reference and returns one key from a field of the object. A list
// mapper lists the dependent kind in the object's namespace and returns one
// key per item, selected by a label whose value is the object's name or by
// a field of the item equal to the object's name. Other bodies fail.
func (m *mapper) extract(rule *Rule) error {
	fn := m.fn
	if fn == nil || fn.Body == nil || len(fn.Type.Params.List) != 2 {
		var at ast.Node = m.f.Name
		if fn != nil {
			at = fn.Name
		}
		return diag(m.fs, at, "mapper", "unsupported mapping function "+rule.Mapping)
	}
	alias := func(path string) string {
		for name, p := range m.imports {
			if p == path {
				return name
			}
		}
		return ""
	}
	m.ctxAlias, m.clientAlias, m.reconcileAlias = alias("context"), alias("sigs.k8s.io/controller-runtime/pkg/client"), alias("sigs.k8s.io/controller-runtime/pkg/reconcile")
	if m.ctxAlias == "" || m.clientAlias == "" || m.reconcileAlias == "" {
		return diag(m.fs, fn.Name, "mapper", "unresolved mapper imports: context, client and reconcile must be imported")
	}
	for _, p := range fn.Type.Params.List {
		if len(p.Names) != 1 {
			return diag(m.fs, p, "mapper", "unsupported mapper parameters")
		}
	}
	m.ctxName, m.objName = fn.Type.Params.List[0].Names[0].Name, fn.Type.Params.List[1].Names[0].Name
	var err error
	switch len(fn.Body.List) {
	case 3:
		err = m.field(rule)
	case 5:
		err = m.list(rule)
	default:
		err = diag(m.fs, fn.Name, "mapper", "unsupported mapping function "+rule.Mapping)
	}
	if err != nil {
		return err
	}
	rule.MappingSHA256 = goflow.Hash([]byte(goflow.Code(fn)))
	return nil
}

// header renders the signature of the map function for the expected source.
func (m *mapper) header() string {
	recv := ""
	if r := m.fn.Recv; r != nil && len(r.List) == 1 {
		names := ""
		if len(r.List[0].Names) == 1 {
			names = r.List[0].Names[0].Name + " "
		}
		recv = "(" + names + goflow.Code(r.List[0].Type) + ") "
	}
	return fmt.Sprintf("func %s%s(%s %s.Context,%s %s.Object)[]%s.Request", recv, m.fn.Name.Name, m.ctxName, m.ctxAlias, m.objName, m.clientAlias, m.reconcileAlias)
}

// matches reports whether the map function prints as body under its header.
func (m *mapper) matches(body string) (bool, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "expected.go", "package p\n"+m.header()+"{\n"+body+"\n}", 0)
	if err != nil {
		return false, err
	}
	normalized := *m.fn
	normalized.Doc = nil
	return goflow.Code(parsed.Decls[0]) == goflow.Code(&normalized), nil
}

// keyType returns the request key type used in n: client.ObjectKey unless
// a NamespacedName literal names an accepted key type.
func (m *mapper) keyType(n ast.Node) string {
	keyType := m.clientAlias + ".ObjectKey"
	ast.Inspect(n, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "NamespacedName" {
			if lit, ok := kv.Value.(*ast.CompositeLit); ok && keyTypes[m.qualified(lit.Type)] {
				keyType = goflow.Code(lit.Type)
			}
		}
		return true
	})
	return keyType
}

// fieldPath splits a selector such as item.Spec.Ref into its root and a
// field path of identifiers.
func fieldPath(code string) (string, []string, bool) {
	parts := strings.Split(code, ".")
	if len(parts) < 2 {
		return "", nil, false
	}
	for _, part := range parts {
		if !token.IsIdentifier(part) {
			return "", nil, false
		}
	}
	return parts[0], parts[1:], true
}

// field recognizes the type-assert, reject-empty-reference, return-one-key
// mapper. The selected Name field is extracted, never pinned to an expected
// business field. Namespace transformations need another pattern.
func (m *mapper) field(rule *Rule) error {
	fs, fn := m.fs, m.fn
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
	if !ok || m.qualified(typ) != rule.GoType {
		return diag(fs, typ, "mapper", "mapper type differs from watched type")
	}
	ret, ok := fn.Body.List[2].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return diag(fs, fn.Body.List[2], "mapper", "unsupported mapper return")
	}
	var path string
	ast.Inspect(ret, func(n ast.Node) bool {
		if kv, ok := n.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Name" {
				path = goflow.Code(kv.Value)
			}
		}
		return true
	})
	root, parts, ok := fieldPath(path)
	if !ok || root != local.Name {
		return diag(fs, ret, "mapper", "mapper request name must be an object field path")
	}
	body := fmt.Sprintf(` %s,%s:=%s.(*%s.%s)
 if !%s||%s==""{return nil}
 return []%s.Request{{NamespacedName:%s{Namespace:%s.Namespace,Name:%s}}}`, local.Name, valid.Name, m.objName, pkg.Name, typ.Sel.Name, valid.Name, path, m.reconcileAlias, m.keyType(ret), local.Name, path)
	ok, err := m.matches(body)
	if err != nil {
		return err
	}
	if !ok {
		return diag(fs, fn.Name, "mapper", "unsupported mapping control flow in "+fn.Name.Name)
	}
	rule.NamePath = parts
	return nil
}

// list recognizes the list mapper:
//
//	items := &pkg.KindList{}
//	if err := r.Client.List(ctx, items, client.InNamespace(obj.GetNamespace()), selector...); err != nil {
//		return nil
//	}
//	requests := make([]reconcile.Request, 0, len(items.Items)) // or var requests []reconcile.Request
//	for _, item := range items.Items {
//		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: item.Namespace, Name: item.Name}})
//	}
//	return requests
//
// The selector is either client.MatchingLabels{key: obj.GetName()} with a
// constant key, or absent with the append guarded by
// `if item.<path> == obj.GetName()`. The client may be any field of the
// receiver; the names of locals are free.
func (m *mapper) list(rule *Rule) error {
	fs, fn := m.fs, m.fn
	unsupported := func(n ast.Node, reason string) error {
		return diag(fs, n, "mapper", reason)
	}
	// items := &pkg.KindList{}
	decl, ok := fn.Body.List[0].(*ast.AssignStmt)
	if !ok || decl.Tok != token.DEFINE || len(decl.Lhs) != 1 || len(decl.Rhs) != 1 {
		return unsupported(fn.Body.List[0], "list mapper must start with a list object")
	}
	items, ok := decl.Lhs[0].(*ast.Ident)
	ptr, isPtr := decl.Rhs[0].(*ast.UnaryExpr)
	if !ok || !isPtr || ptr.Op != token.AND {
		return unsupported(decl, "list mapper must start with a list object")
	}
	lit, ok := ptr.X.(*ast.CompositeLit)
	if !ok || len(lit.Elts) != 0 || !strings.HasSuffix(m.qualified(lit.Type), "List") {
		return unsupported(decl, "list mapper must start with an empty list literal")
	}
	dependent, ok := m.resources[strings.TrimSuffix(m.qualified(lit.Type), "List")]
	if !ok {
		return unsupported(lit, "unknown listed resource "+m.qualified(lit.Type))
	}
	// if err := r.Client.List(...); err != nil { return nil }
	check, ok := fn.Body.List[1].(*ast.IfStmt)
	if !ok || check.Init == nil {
		return unsupported(fn.Body.List[1], "list mapper must list with the client")
	}
	init, ok := check.Init.(*ast.AssignStmt)
	if !ok || len(init.Rhs) != 1 {
		return unsupported(check, "list mapper must list with the client")
	}
	call, ok := init.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) < 3 {
		return unsupported(check, "list mapper must list with the client")
	}
	method, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || method.Sel.Name != "List" {
		return unsupported(call, "list mapper must call List")
	}
	listClient, ok := method.X.(*ast.SelectorExpr)
	if !ok || fn.Recv == nil || len(fn.Recv.List[0].Names) != 1 {
		return unsupported(method.X, "list mapper must list through a client field of the receiver")
	}
	if recv, ok := listClient.X.(*ast.Ident); !ok || recv.Name != fn.Recv.List[0].Names[0].Name {
		return unsupported(method.X, "list mapper must list through a client field of the receiver")
	}
	selector, label := "", ""
	switch len(call.Args) {
	case 3:
	case 4:
		labels, ok := call.Args[3].(*ast.CompositeLit)
		if !ok || m.qualified(labels.Type) != "sigs.k8s.io/controller-runtime/pkg/client.MatchingLabels" || len(labels.Elts) != 1 {
			return unsupported(call.Args[3], "list selector must be client.MatchingLabels with one label")
		}
		kv, ok := labels.Elts[0].(*ast.KeyValueExpr)
		if !ok {
			return unsupported(labels, "list selector must be client.MatchingLabels with one label")
		}
		if label, ok = m.constant(kv.Key); !ok || label == "" {
			return unsupported(kv.Key, "label key must be a string constant")
		}
		selector = fmt.Sprintf(", %s.MatchingLabels{%s: %s.GetName()}", m.clientAlias, goflow.Code(kv.Key), m.objName)
	default:
		return unsupported(call, "list mapper takes at most one label selector")
	}
	// requests := make(...) or var requests []reconcile.Request
	var requests string
	switch s := fn.Body.List[2].(type) {
	case *ast.AssignStmt:
		if id, ok := s.Lhs[0].(*ast.Ident); ok && len(s.Lhs) == 1 {
			requests = id.Name
		}
	case *ast.DeclStmt:
		if gen, ok := s.Decl.(*ast.GenDecl); ok && len(gen.Specs) == 1 {
			if v, ok := gen.Specs[0].(*ast.ValueSpec); ok && len(v.Names) == 1 {
				requests = v.Names[0].Name
			}
		}
	}
	if requests == "" {
		return unsupported(fn.Body.List[2], "list mapper must declare a request slice")
	}
	declare := fmt.Sprintf("%s := make([]%s.Request, 0, len(%s.Items))", requests, m.reconcileAlias, items.Name)
	if _, ok := fn.Body.List[2].(*ast.DeclStmt); ok {
		declare = fmt.Sprintf("var %s []%s.Request", requests, m.reconcileAlias)
	}
	// for _, item := range items.Items { ... }
	loop, ok := fn.Body.List[3].(*ast.RangeStmt)
	if !ok || loop.Value == nil || len(loop.Body.List) != 1 {
		return unsupported(fn.Body.List[3], "list mapper must range over the listed items")
	}
	item, ok := loop.Value.(*ast.Ident)
	if !ok {
		return unsupported(loop.Value, "list mapper must range over the listed items")
	}
	add := fmt.Sprintf("%s = append(%s, %s.Request{NamespacedName: %s{Namespace: %s.Namespace, Name: %s.Name}})", requests, requests, m.reconcileAlias, m.keyType(loop), item.Name, item.Name)
	var path []string
	if guard, ok := loop.Body.List[0].(*ast.IfStmt); ok {
		if label != "" {
			return unsupported(guard, "list mapper selects by a label or a field, not both")
		}
		cond, ok := guard.Cond.(*ast.BinaryExpr)
		if !ok || cond.Op != token.EQL {
			return unsupported(guard, "list filter must compare an item field with the object name")
		}
		root, parts, ok := fieldPath(goflow.Code(cond.X))
		if !ok || root != item.Name {
			return unsupported(cond.X, "list filter must compare an item field with the object name")
		}
		path = parts
		add = fmt.Sprintf("if %s == %s.GetName() { %s }", goflow.Code(cond.X), m.objName, add)
	} else if label == "" {
		return unsupported(loop, "list mapper must select by a label or a field")
	}
	body := fmt.Sprintf(`%s := &%s{}
if err := %s.List(%s, %s, %s.InNamespace(%s.GetNamespace())%s); err != nil { return nil }
%s
for _, %s := range %s.Items { %s }
return %s`, items.Name, goflow.Code(lit.Type), goflow.Code(listClient), m.ctxName, items.Name, m.clientAlias, m.objName, selector, declare, item.Name, items.Name, add, requests)
	ok, err := m.matches(body)
	if err != nil {
		return err
	}
	if !ok {
		return diag(fs, fn.Name, "mapper", "unsupported mapping control flow in "+fn.Name.Name)
	}
	rule.NamePath, rule.Dependent, rule.Label, rule.DependentPath = nil, &dependent, label, path
	return nil
}
