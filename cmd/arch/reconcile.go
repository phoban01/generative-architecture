package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"path"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/catalog"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/project"
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
	Effect, Operation, Consistency string
}

// operations maps a client method name to its operation key in the summary
// library. Only operations with summary bindings are listed; other methods
// are unsupported and fail with the callee named.
var operations = map[string]string{"Get": ir.OpGet, "Update": ir.OpUpdate, "Create": ir.OpCreate, "Delete": ir.OpDelete}

// clientEffects resolves the declared client effects. The consistency
// follows the controller-runtime injection pattern unless declared: a read
// through the manager's client (receiver Client) is served by the informer
// cache, any other call goes to the API server.
func clientEffects(decl project.Controller) (map[goflow.Callee]clientEffect, error) {
	out := map[goflow.Callee]clientEffect{}
	for _, e := range decl.Effects {
		pkg, receiver, method, err := e.Symbol()
		if err != nil {
			return nil, err
		}
		op, ok := operations[method]
		if !ok {
			return nil, fmt.Errorf("controller %s: %s: method %s is not supported; supported methods are Get, Update, Create and Delete", decl.Name, e.Call, method)
		}
		consistency := e.Consistency
		if consistency == "" {
			consistency = ir.ConsistencyDirect
			if op == ir.OpGet && receiver == "Client" {
				consistency = ir.ConsistencyCached
			}
		}
		out[goflow.Callee{Package: pkg, Receiver: receiver, Method: method}] = clientEffect{Effect: e.Effect, Operation: op, Consistency: consistency}
	}
	return out, nil
}

// extraction holds the model names extraction binds Go objects to.
type extraction struct {
	decl     project.Controller
	effects  map[goflow.Callee]clientEffect
	keys     map[string]string // key field symbol -> abstraction object
	cat      *catalog.Catalog
	info     *types.Info
	body     *ast.BlockStmt
	locals   map[string]abstraction.Resource // Go object local -> its resource
	resource map[ir.GVK]abstraction.Resource
	pkg      *packages.Package // the package of Reconcile, for group-version lookups
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
	x := &extraction{decl: decl, effects: effects, keys: map[string]string{requestKey: decl.Key}, cat: cat, info: l.Package.TypesInfo, body: l.Func.Body, locals: map[string]abstraction.Resource{}, resource: map[ir.GVK]abstraction.Resource{}, pkg: l.Package}
	for symbol, object := range decl.ObjectKeys {
		x.keys[symbol] = object
	}
	for _, r := range abs.Spec.Resources {
		x.resource[r.GVK] = r
	}
	scope := map[string]expr.Symbol{}
	ast.Inspect(l.Func.Body, func(n ast.Node) bool {
		a, ok := n.(*ast.AssignStmt)
		if !ok || err != nil || len(a.Lhs) != 1 {
			return true
		}
		id, ok := a.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		obj, ok := x.info.Defs[id].(*types.Var)
		if !ok {
			return true
		}
		gvk, ok, gvErr := x.resourceOf(obj.Type())
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
		for _, f := range r.Fields {
			v := x.localVariable(id.Name, f)
			scope[v] = expr.Symbol{Type: f.Type, Quint: v}
		}
		return true
	})
	if err != nil {
		return nil, goflow.Options{}, err
	}
	opts := goflow.Options{File: l.Path, Scope: scope, Returns: []goflow.Return{{Zero: true, Requeue: true}, {Error: true}}}
	opts.Call = x.call
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
	r, ok := x.locals[root]
	if !ok {
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
	e, ok := x.effects[c]
	if !ok {
		return goflow.Node{}, false, nil
	}
	if len(call.Args) < 2 {
		return goflow.Node{}, false, fmt.Errorf("%s: unexpected arity", c)
	}
	objArg := call.Args[len(call.Args)-1]
	local, r, err := x.local(objArg)
	if err != nil {
		return goflow.Node{}, false, err
	}
	var object string
	switch e.Operation {
	case ir.OpGet:
		if object, err = x.key(call.Args[1]); err != nil {
			return goflow.Node{}, false, err
		}
	default:
		// A write addresses the object the local was read from.
		if object, err = x.readInto(local); err != nil {
			return goflow.Node{}, false, err
		}
	}
	if !hasObject(r, object) {
		return goflow.Node{}, false, fmt.Errorf("%s: object %s is not an abstracted %s object", c, object, r.GVK.Kind)
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
	case e.Operation == ir.OpDelete:
		// delete reads and clears the existence flag only.
	default:
		return goflow.Node{}, false, fmt.Errorf("%s: no bindings for operation %s", c, e.Operation)
	}
	return goflow.Node{Kind: e.Operation, Effect: e.Effect, Consistency: e.Consistency, Bindings: b, Vectors: vectors}, true, nil
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
func (x *extraction) key(arg ast.Expr) (string, error) {
	sel, ok := ast.Unparen(arg).(*ast.SelectorExpr)
	if !ok {
		return "", fmt.Errorf("object key must be a field selection, got %s", goflow.Code(arg))
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
		e, ok := x.effects[goflow.Callee{Package: s.Obj().Pkg().Path(), Receiver: named.Obj().Name(), Method: s.Obj().Name()}]
		if !ok || e.Operation != ir.OpGet {
			return true
		}
		if l, _, err := x.local(call.Args[2]); err != nil || l != local {
			return true
		}
		if object, err := x.key(call.Args[1]); err == nil {
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
