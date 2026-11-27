package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/catalog"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/project"
	"github.com/phoban01/generative-architecture/pkg/summaries"
)

// This file lowers a reconciler's Reconcile method with the typed frontend.
// Client calls resolve by callee symbol to the effects the Project document
// declares, and fields of object locals resolve by JSON path to model
// variables named after the Abstraction document.

// requestKey is the field of the reconcile request that carries the key of
// the object the controller is driven by.
const requestKey = "sigs.k8s.io/controller-runtime/pkg/reconcile.Request.NamespacedName"

// versionPath is the JSON path of the object version the summaries compare.
var versionPath = []string{"metadata", "resourceVersion"}

// clientEffect is what a call of one client method means: the effect it
// performs, its operation key in the summary library and its consistency.
type clientEffect struct {
	Kind                           string
	Effect, Operation, Consistency string
}

// operations maps a client method name to its operation key in the summary
// library. Only operations with summary bindings are listed; other methods
// are unsupported and fail with the callee named.
var operations = map[string]string{"Get": ir.OpGet, "List": ir.OpList, "Update": ir.OpUpdate, "Create": ir.OpCreate, "Delete": ir.OpDelete, "Patch": ir.OpPatch}

// statusWriters are the receivers of a call through Client.Status().
var statusWriters = map[string]bool{"SubResourceWriter": true, "StatusWriter": true}

// clientEffects resolves the declared client effects. The consistency
// follows the controller-runtime injection pattern unless declared: a read
// through the manager's client (receiver Client) is served by the informer
// cache, any other call goes to the API server. A call through
// Client.Status() is the status subresource operation, status.update.
func clientEffects(decl project.Controller) (map[goflow.Callee][]clientEffect, error) {
	out := map[goflow.Callee][]clientEffect{}
	for _, e := range decl.Effects {
		pkg, receiver, method, err := e.Symbol()
		if err != nil {
			return nil, err
		}
		op, ok := operations[method]
		if !ok {
			return nil, fmt.Errorf("controller %s: %s: method %s is not supported; supported methods are Get, List, Update, Create, Delete, Status().Update and Status().Patch", decl.Name, e.Call, method)
		}
		if op == ir.OpPatch && !statusWriters[receiver] {
			return nil, fmt.Errorf("controller %s: %s: Patch is supported on the status subresource only", decl.Name, e.Call)
		}
		if statusWriters[receiver] {
			if op != ir.OpUpdate && op != ir.OpPatch {
				return nil, fmt.Errorf("controller %s: %s: status %s is not supported; only Status().Update and Status().Patch are", decl.Name, e.Call, method)
			}
			op = summaries.OperationKey(op, "status")
		}
		consistency := e.Consistency
		if consistency == "" {
			consistency = ir.ConsistencyDirect
			if op == ir.OpGet && receiver == "Client" {
				consistency = ir.ConsistencyCached
			}
		}
		callee := goflow.Callee{Package: pkg, Receiver: receiver, Method: method}
		out[callee] = append(out[callee], clientEffect{Kind: e.Kind, Effect: e.Effect, Operation: op, Consistency: consistency})
	}
	return out, nil
}

// extraction holds the model names extraction binds Go objects to.
type extraction struct {
	decl    project.Controller
	effects map[goflow.Callee][]clientEffect
	keys    map[string]string // key field symbol -> abstraction object
	cat     *catalog.Catalog
	info    *types.Info
	body    *ast.BlockStmt
	locals  map[string]abstraction.Resource // Go object local -> its resource
	// lists marks the object locals that are list types; their Items are
	// the model list listSymbol(local).
	lists map[string]bool
	// elements are the loop elements that range over a list of objects:
	// identities of the resource (ADR-0011).
	elements map[string]abstraction.Resource
	resource map[ir.GVK]abstraction.Resource
	pkg      *packages.Package // the package of Reconcile, for group-version lookups
	// goTypes records the Go type of each resource the reconciler holds in
	// a local, for the replay adapter's scheme.
	goTypes map[ir.GVK]goType
}

// localVariable is the model variable holding the selected field f of a Go
// object local: the local the project declares for it, else
// <local>_<name> for a named field and <local>_<path elements> otherwise.
// The project names the field by the path as the abstraction writes it.
func (x *extraction) localVariable(local string, f abstraction.Field) string {
	if v, ok := x.decl.Locals[local+"."+abstraction.PathKey(f.Path)]; ok {
		return v
	}
	if f.Name != "" {
		return local + "_" + f.Name
	}
	return local + "_" + strings.Join(abstraction.JSONPath(f.Path), "_")
}

// extractOptions builds the typed-frontend options for Reconcile.
func extractOptions(l goflow.Loaded, abs abstraction.Abstraction, decl project.Controller, cat *catalog.Catalog) (*extraction, goflow.Options, error) {
	effects, err := clientEffects(decl)
	if err != nil {
		return nil, goflow.Options{}, err
	}
	x := &extraction{decl: decl, effects: effects, keys: map[string]string{requestKey: decl.Key}, cat: cat, info: l.Package.TypesInfo, body: l.Func.Body, locals: map[string]abstraction.Resource{}, lists: map[string]bool{}, elements: map[string]abstraction.Resource{}, resource: map[ir.GVK]abstraction.Resource{}, pkg: l.Package, goTypes: map[ir.GVK]goType{}}
	for symbol, object := range decl.ObjectKeys {
		x.keys[symbol] = object
	}
	for _, r := range abs.Spec.Resources {
		x.resource[r.GVK] = r
	}
	scope := map[string]expr.Symbol{}
	// The abstraction's state variables are in scope for the reads a loop
	// element lowers to (see elementField).
	for name, v := range abs.StateVariables() {
		scope[name] = expr.Symbol{Type: v.Type, Quint: name, Domain: v.Domain}
	}
	// A declared abstraction local is the model symbol of the Go local of
	// the same name: a counter, or a list of identities a loop builds.
	for _, local := range abs.Spec.Locals {
		scope[local.Name] = expr.Symbol{Type: local.Type, Quint: local.Name}
	}
	// declare records a Go local of an abstracted resource type, or of
	// the list type of one, and puts its selected fields in scope.
	declare := func(id *ast.Ident) bool {
		obj, ok := x.info.Defs[id].(*types.Var)
		if !ok {
			return true
		}
		t := obj.Type()
		item, list := listItem(t)
		if list {
			t = item
		}
		gvk, ok, gvErr := x.resourceOf(t)
		if gvErr != nil {
			err = goflow.NewDiagnostic(l.Package.Fset, id, "group-version", gvErr.Error())
			return false
		}
		if !ok {
			return true
		}
		r, ok := x.resource[gvk]
		if !ok {
			err = goflow.NewDiagnostic(l.Package.Fset, id, "local "+id.Name, fmt.Sprintf("has type %s, which the abstraction does not select", gvk))
			return false
		}
		x.locals[id.Name] = r
		if gt, ok := namedType(t); ok {
			x.goTypes[gvk] = gt
		}
		if list {
			x.lists[id.Name] = true
			scope[listSymbol(id.Name)] = expr.Symbol{Type: abstraction.TypeList, Quint: listSymbol(id.Name)}
			return true
		}
		for _, f := range r.Fields {
			v := x.localVariable(id.Name, f)
			scope[v] = expr.Symbol{Type: f.Type, Quint: v, Domain: r.Domain(f)}
		}
		return true
	}
	ast.Inspect(l.Func.Body, func(n ast.Node) bool {
		if err != nil {
			return false
		}
		switch s := n.(type) {
		case *ast.AssignStmt:
			if len(s.Lhs) != 1 {
				return true
			}
			if id, ok := s.Lhs[0].(*ast.Ident); ok {
				return declare(id)
			}
		case *ast.RangeStmt:
			if id, ok := s.Value.(*ast.Ident); ok && id.Name != "_" {
				if !x.declareElement(id, scope) {
					return false
				}
			}
		case *ast.ValueSpec:
			for _, id := range s.Names {
				if !declare(id) {
					return false
				}
			}
		}
		return true
	})
	if err != nil {
		return nil, goflow.Options{}, err
	}
	opts := goflow.Options{File: l.Path, Scope: scope, Returns: []goflow.Return{{Zero: true, Requeue: true}, {Error: true}}}
	bindings, pure, err := declaredChoices(abs, decl)
	if err != nil {
		return nil, goflow.Options{}, err
	}
	opts.Bindings, opts.Pure = bindings, pure
	opts.Call = x.call
	opts.ElementCall = x.elementCall
	opts.Field = x.field
	opts.MapKeys = x.mapKeys
	return x, opts, nil
}

// resourceOf maps a Go API type (or pointer to one) to its GVK: a type
// declared in k8s.io/api/<group>/<version> by its path (the core group is
// ""), any other type by the group and version its package declares (see
// groupVersion) and the type's name, looked up in the catalog. A package
// that declares neither a GroupVersion variable nor a +groupName marker is
// an error, which the caller locates at the local.
func (x *extraction) resourceOf(t types.Type) (ir.GVK, bool, error) {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return ir.GVK{}, false, nil
	}
	pkg := n.Obj().Pkg()
	if rest, ok := strings.CutPrefix(pkg.Path(), "k8s.io/api/"); ok {
		group, version := path.Split(rest)
		group = strings.TrimSuffix(group, "/")
		if group == "core" {
			group = ""
		}
		return ir.GVK{Group: group, Version: version, Kind: n.Obj().Name()}, true, nil
	}
	if x.cat == nil {
		return ir.GVK{}, false, nil
	}
	kinds := false
	for _, e := range x.cat.Entries() {
		if e.GVK.Kind == n.Obj().Name() {
			kinds = true
		}
	}
	if !kinds {
		return ir.GVK{}, false, nil
	}
	if x.pkg == nil {
		return ir.GVK{}, false, nil
	}
	group, version, ok := groupVersion(x.pkg, pkg.Path())
	if !ok {
		return ir.GVK{}, false, fmt.Errorf("package %s declares no GroupVersion variable and no +groupName marker", pkg.Path())
	}
	e, ok := x.cat.Lookup(ir.GVK{Group: group, Version: version, Kind: n.Obj().Name()})
	if !ok {
		return ir.GVK{}, false, nil
	}
	return e.GVK, true, nil
}

// field maps a selected field of a Go object local to its model variable.
func (x *extraction) field(root string, fieldPath []string) (expr.Expression, bool) {
	if r, ok := x.elements[root]; ok {
		return x.elementField(root, r, fieldPath)
	}
	r, ok := x.locals[root]
	if !ok {
		return "", false
	}
	if x.lists[root] {
		if abstraction.PathKey(fieldPath) == "items" {
			return expr.Expression(listSymbol(root)), true
		}
		return "", false
	}
	for _, f := range r.Fields {
		if abstraction.PathKey(fieldPath) == abstraction.PathKey(abstraction.JSONPath(f.Path)) {
			return expr.Expression(x.localVariable(root, f)), true
		}
	}
	return "", false
}

// call summarizes a client call: its effect, operation, consistency and
// the bindings of the summary placeholders to the addressed object's
// variables and the Go local the object is read into or written from.
func (x *extraction) call(c goflow.Callee, call *ast.CallExpr, _ goflow.Convert) (goflow.Node, bool, error) {
	declared, ok := x.effects[c]
	if !ok {
		return goflow.Node{}, false, nil
	}
	if len(call.Args) < 2 {
		return goflow.Node{}, false, fmt.Errorf("%s: unexpected arity", c)
	}
	objArg := call.Args[len(call.Args)-1]
	if declared[0].Operation == ir.OpList {
		// List(ctx, list, opts...): the options follow the list.
		objArg = call.Args[1]
	}
	if declared[0].Operation == summaries.OperationKey(ir.OpPatch, "status") {
		// Patch(ctx, obj, patch): the summary covers client.Merge, the
		// whole-object merge patch, which carries the object's version.
		if len(call.Args) != 3 || goflow.Code(call.Args[2]) != "client.Merge" {
			return goflow.Node{}, false, fmt.Errorf("%s: only a client.Merge patch is supported, got %s", c, goflow.Code(call))
		}
		objArg = call.Args[1]
	}
	elementArg := ast.Unparen(objArg)
	if u, ok := elementArg.(*ast.UnaryExpr); ok {
		elementArg = ast.Unparen(u.X)
	}
	if id, ok := elementArg.(*ast.Ident); ok {
		if r, ok := x.elements[id.Name]; ok {
			// A call on a loop element fans out over the element's objects.
			ids := make([]string, 0, len(r.Objects))
			for _, o := range r.Objects {
				ids = append(ids, o.ID)
			}
			return goflow.Node{}, false, &goflow.FanOut{Root: id.Name, IDs: ids}
		}
	}
	local, r, err := x.local(objArg)
	if err != nil {
		return goflow.Node{}, false, err
	}
	e, ok := effectFor(declared, r.GVK.Kind)
	if !ok {
		return goflow.Node{}, false, fmt.Errorf("%s is called with a %s, but no effect is declared for that kind", c, r.GVK.Kind)
	}
	if e.Operation == ir.OpList {
		if !x.lists[local] {
			return goflow.Node{}, false, fmt.Errorf("%s: %s is not a list local", c, local)
		}
		n, err := x.listCall(c, call, local, r, e)
		return n, err == nil, err
	}
	if x.lists[local] {
		return goflow.Node{}, false, fmt.Errorf("%s: %s is a list local; only List takes one", c, local)
	}
	var object string
	switch e.Operation {
	case ir.OpGet:
		if object, err = x.key(call.Args[1], r); err != nil {
			return goflow.Node{}, false, err
		}
	default:
		// A write addresses the object the local was read from.
		if object, err = x.readInto(local); err != nil {
			return goflow.Node{}, false, err
		}
	}
	n, err := x.bindings(c, e, r, object, local)
	return n, err == nil, err
}

func hasObject(r abstraction.Resource, id string) bool {
	for _, o := range r.Objects {
		if o.ID == id {
			return true
		}
	}
	return false
}

// local resolves the object argument of a client call to a Go object local.
func (x *extraction) local(arg ast.Expr) (string, abstraction.Resource, error) {
	if u, ok := ast.Unparen(arg).(*ast.UnaryExpr); ok {
		arg = u.X
	}
	id, ok := ast.Unparen(arg).(*ast.Ident)
	if !ok {
		return "", abstraction.Resource{}, fmt.Errorf("client call object must be a local, got %s", goflow.Code(arg))
	}
	r, ok := x.locals[id.Name]
	if !ok {
		return "", abstraction.Resource{}, fmt.Errorf("client call object %s is not an abstracted object local", id.Name)
	}
	return id.Name, r, nil
}

// key resolves a key argument, a field selection, to an abstraction object
// through the symbol of the selected field.
// objectKeyType is the type of client.ObjectKey.
const objectKeyType = "k8s.io/apimachinery/pkg/types.NamespacedName"

// key resolves the key argument of a read to the abstraction object it
// addresses: a key field declared under objectKeys (the reconcile
// request's key addresses the controller's key), or a client.ObjectKey
// literal whose Name and Namespace the abstraction fixes (see keyValue),
// matched against the objects of resource r.
func (x *extraction) key(arg ast.Expr, r abstraction.Resource) (string, error) {
	if lit, ok := ast.Unparen(arg).(*ast.CompositeLit); ok {
		return x.keyLiteral(lit, r)
	}
	sel, ok := ast.Unparen(arg).(*ast.SelectorExpr)
	if !ok {
		return "", fmt.Errorf("object key must be a field selection or a client.ObjectKey literal, got %s", goflow.Code(arg))
	}
	s, ok := x.info.Selections[sel]
	if !ok || s.Kind() != types.FieldVal {
		return "", fmt.Errorf("object key %s is not a field", goflow.Code(arg))
	}
	recv := s.Recv()
	if p, ok := types.Unalias(recv).(*types.Pointer); ok {
		recv = p.Elem()
	}
	n, ok := types.Unalias(recv).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return "", fmt.Errorf("object key %s is not a field of a named type", goflow.Code(arg))
	}
	symbol := n.Obj().Pkg().Path() + "." + n.Obj().Name() + "." + s.Obj().Name()
	object, ok := x.keys[symbol]
	if !ok {
		return "", fmt.Errorf("object key %s addresses no declared object; declare it under objectKeys", symbol)
	}
	return object, nil
}

// keyLiteral resolves a client.ObjectKey literal to the one object of r
// with the literal's name, and namespace when the literal sets one.
func (x *extraction) keyLiteral(lit *ast.CompositeLit, r abstraction.Resource) (string, error) {
	code := goflow.Code(lit)
	if t := x.info.TypeOf(lit); t == nil || types.TypeString(types.Unalias(t), nil) != objectKeyType {
		return "", fmt.Errorf("object key %s is not a client.ObjectKey literal", code)
	}
	var name, namespace string
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return "", fmt.Errorf("object key %s must set its fields by name", code)
		}
		value, err := x.keyValue(kv.Value)
		if err != nil {
			return "", fmt.Errorf("object key %s: %v", code, err)
		}
		switch goflow.Code(kv.Key) {
		case "Name":
			name = value
		case "Namespace":
			namespace = value
		default:
			return "", fmt.Errorf("object key %s sets %s; only Name and Namespace are object key fields", code, goflow.Code(kv.Key))
		}
	}
	if name == "" {
		return "", fmt.Errorf("object key %s sets no Name", code)
	}
	var found []string
	for _, o := range r.Objects {
		if o.Name == name && (namespace == "" || o.Namespace == namespace) {
			found = append(found, o.ID)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("object key %s addresses %d %s objects %v; expected exactly one", code, len(found), r.GVK.Kind, found)
	}
	return found[0], nil
}

// keyValue resolves one field of an object key literal to a string the
// abstraction fixes: a constant, a field of a Go local that was read from
// an abstraction object, or a concatenation of those. The object's name
// and namespace come from the object; any other field must have a value
// domain of one value.
func (x *extraction) keyValue(v ast.Expr) (string, error) {
	if field, ok := x.requestField(v); ok {
		// The request names the controller's key object.
		_, o, found := x.keyObject()
		if !found {
			return "", fmt.Errorf("the controller's key %s is not an abstracted object", x.decl.Key)
		}
		if field == "Name" {
			return o.Name, nil
		}
		return o.Namespace, nil
	}
	if tv, ok := x.info.Types[v]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
		return constant.StringVal(tv.Value), nil
	}
	if b, ok := ast.Unparen(v).(*ast.BinaryExpr); ok && b.Op == token.ADD {
		left, err := x.keyValue(b.X)
		if err != nil {
			return "", err
		}
		right, err := x.keyValue(b.Y)
		if err != nil {
			return "", err
		}
		return left + right, nil
	}
	root, path, err := goflow.FieldPath(x.info, v)
	if err != nil {
		return "", err
	}
	r, ok := x.locals[root]
	if !ok {
		return "", fmt.Errorf("%s is not a local of an abstracted type", root)
	}
	id, err := x.readInto(root)
	if err != nil {
		return "", err
	}
	o := objectOf(r, id)
	switch abstraction.PathKey(path) {
	case "metadata.name":
		return o.Name, nil
	case "metadata.namespace":
		return o.Namespace, nil
	}
	for _, f := range r.Fields {
		if abstraction.PathKey(abstraction.JSONPath(f.Path)) != abstraction.PathKey(path) {
			continue
		}
		domain := r.ValueDomains[f.Key()]
		if len(domain) != 1 {
			return "", fmt.Errorf("%s has %d values in the abstraction; a key field needs exactly one", goflow.Code(v), len(domain))
		}
		value, err := strconv.Unquote(domain[0])
		if err != nil {
			return "", fmt.Errorf("%s: value %s is not a string literal", goflow.Code(v), domain[0])
		}
		return value, nil
	}
	return "", fmt.Errorf("the abstraction does not select %s", goflow.Code(v))
}

// objectOf returns the object of r with the given id.
func objectOf(r abstraction.Resource, id string) abstraction.Object {
	for _, o := range r.Objects {
		if o.ID == id {
			return o
		}
	}
	return abstraction.Object{}
}

// readInto finds the object a Go local was read into: the key of the
// unique read whose object argument is that local.
func (x *extraction) readInto(local string) (string, error) {
	var found []string
	ast.Inspect(x.body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		if !ok {
			return true
		}
		s, ok := x.info.Selections[sel]
		if !ok || s.Kind() != types.MethodVal || s.Obj().Pkg() == nil {
			return true
		}
		recv := types.Unalias(s.Recv())
		if p, ok := recv.(*types.Pointer); ok {
			recv = types.Unalias(p.Elem())
		}
		named, ok := recv.(*types.Named)
		if !ok {
			return true
		}
		declared, ok := x.effects[goflow.Callee{Package: s.Obj().Pkg().Path(), Receiver: named.Obj().Name(), Method: s.Obj().Name()}]
		if !ok || declared[0].Operation != ir.OpGet {
			return true
		}
		l, r, err := x.local(call.Args[2])
		if err != nil || l != local {
			return true
		}
		if object, err := x.key(call.Args[1], r); err == nil {
			found = append(found, object)
		}
		return true
	})
	sort.Strings(found)
	if len(found) != 1 {
		return "", fmt.Errorf("local %s is written but read from %d objects %v; expected exactly one", local, len(found), found)
	}
	return found[0], nil
}

// mapKeys lists the selected keys of the map at jsonPath on the object
// local root: the last element of every selected field path whose parent
// is jsonPath. goflow clears the keys a map literal omits, so a replaced
// map does not keep stale values.
func (x *extraction) mapKeys(root string, jsonPath []string) []string {
	r, ok := x.locals[root]
	if !ok {
		return nil
	}
	var keys []string
	for _, f := range r.Fields {
		p := abstraction.JSONPath(f.Path)
		if len(p) == len(jsonPath)+1 && abstraction.PathKey(p[:len(p)-1]) == abstraction.PathKey(jsonPath) {
			keys = append(keys, p[len(p)-1])
		}
	}
	return keys
}

// statusFields names the object variables of the selected status fields
// of object and the local variables they are written from.
func statusFields(r abstraction.Resource, object, local string, x *extraction) ([]string, []string) {
	var status, values []string
	for _, f := range r.Fields {
		if p := abstraction.JSONPath(f.Path); len(p) > 0 && p[0] == "status" {
			status = append(status, abstraction.FieldVariable(r.GVK.Kind, object, f))
			values = append(values, x.localVariable(local, f))
		}
	}
	return status, values
}

// effectFor picks the declaration for a call made with an object of kind:
// the one declared for that kind, else the one declared for every kind.
func effectFor(declared []clientEffect, kind string) (clientEffect, bool) {
	var any clientEffect
	var found bool
	for _, e := range declared {
		switch e.Kind {
		case kind:
			return e, true
		case "":
			any, found = e, true
		}
	}
	return any, found
}

// goType is a Go type by import path and name.
type goType struct{ pkg, name string }

// namedType returns the named type behind t, or the pointer to one.
func namedType(t types.Type) (goType, bool) {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return goType{}, false
	}
	return goType{pkg: n.Obj().Pkg().Path(), name: n.Obj().Name()}, true
}

// receiverFields lists the exported struct fields of the reconciler's
// receiver type by name and fully qualified type, for the replay adapter.
func receiverFields(l goflow.Loaded, receiver string) []ir.Field {
	obj := l.Package.Types.Scope().Lookup(receiver)
	if obj == nil {
		return nil
	}
	st, ok := types.Unalias(obj.Type().Underlying()).(*types.Struct)
	if !ok {
		return nil
	}
	var out []ir.Field
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f.Exported() {
			out = append(out, ir.Field{Name: f.Name(), Type: types.TypeString(types.Unalias(f.Type()), nil)})
		}
	}
	return out
}

// bindings builds the node of a client call on object, read into or
// written from the Go local, with the summary placeholders bound to the
// object's variables.
func (x *extraction) bindings(c goflow.Callee, e clientEffect, r abstraction.Resource, object, local string) (goflow.Node, error) {
	if !hasObject(r, object) {
		return goflow.Node{}, fmt.Errorf("%s: object %s is not an abstracted %s object", c, object, r.GVK.Kind)
	}
	// The summary placeholders that stand for the object's content bind one
	// variable per selected field, in field order (see goflow.Node.Vectors).
	values := make([]string, 0, len(r.Fields))
	snapshots := make([]string, 0, len(r.Fields))
	cachedValues := make([]string, 0, len(r.Fields))
	for _, f := range r.Fields {
		value := abstraction.FieldVariable(r.GVK.Kind, object, f)
		values = append(values, value)
		snapshots = append(snapshots, x.localVariable(local, f))
		cachedValues = append(cachedValues, adaptergen.CachedVariable(value))
	}
	version := abstraction.VariableName(r.GVK.Kind, object, versionPath)
	b := map[string]string{
		"$exists":          abstraction.ExistsVariable(r.GVK.Kind, object),
		"$snapshotVersion": x.localVariable(local, abstraction.Field{Path: versionPath}),
	}
	vectors := map[string][]string{}
	switch {
	case e.Operation == ir.OpGet && e.Consistency == ir.ConsistencyCached:
		// Objects are neither created nor deleted while the model runs, so
		// the cache's view of existence is the API server's.
		b["$cacheVersion"] = adaptergen.CachedVariable(version)
		vectors["$cache"], vectors["$snapshot"] = cachedValues, snapshots
	case e.Operation == ir.OpGet:
		b["$resourceVersion"] = version
		vectors["$object"], vectors["$snapshot"] = values, snapshots
	case e.Operation == ir.OpUpdate, e.Operation == ir.OpCreate:
		b["$resourceVersion"] = version
		vectors["$object"], vectors["$value"] = values, snapshots
	case e.Operation == summaries.OperationKey(ir.OpUpdate, "status"), e.Operation == summaries.OperationKey(ir.OpPatch, "status"):
		// A status update replaces the selected status fields with the
		// local's status fields and leaves the other fields alone.
		b["$resourceVersion"] = version
		status, statusValues := statusFields(r, object, local, x)
		if len(status) == 0 {
			return goflow.Node{}, fmt.Errorf("%s: the abstraction selects no status field of %s", c, r.GVK.Kind)
		}
		vectors["$status"], vectors["$statusValue"] = status, statusValues
	case e.Operation == ir.OpDelete:
		// delete reads and clears the existence flag only.
	default:
		return goflow.Node{}, fmt.Errorf("%s: no bindings for operation %s", c, e.Operation)
	}
	return goflow.Node{Kind: e.Operation, Effect: e.Effect, Consistency: e.Consistency, Bindings: b, Vectors: vectors}, nil
}
