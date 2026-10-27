package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"path"
	"sort"
	"strings"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/catalog"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/project"
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
var operations = map[string]string{"Get": ir.OpGet, "Update": ir.OpUpdate}

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
			return nil, fmt.Errorf("controller %s: %s: method %s is not supported; supported methods are Get and Update", decl.Name, e.Call, method)
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
}

// localVariable is the model variable holding a selected field of a Go
// object local.
func (x *extraction) localVariable(local string, fieldPath []string) string {
	if v, ok := x.decl.Locals[local+"."+abstraction.PathKey(fieldPath)]; ok {
		return v
	}
	return local + "_" + strings.Join(fieldPath, "_")
}

// extractOptions builds the typed-frontend options for Reconcile.
func extractOptions(l goflow.Loaded, abs abstraction.Abstraction, decl project.Controller, cat *catalog.Catalog) (*extraction, goflow.Options, error) {
	effects, err := clientEffects(decl)
	if err != nil {
		return nil, goflow.Options{}, err
	}
	x := &extraction{decl: decl, effects: effects, keys: map[string]string{requestKey: decl.Key}, cat: cat, info: l.Package.TypesInfo, body: l.Func.Body, locals: map[string]abstraction.Resource{}, resource: map[ir.GVK]abstraction.Resource{}}
	for symbol, object := range decl.ObjectKeys {
		x.keys[symbol] = object
	}
	for _, r := range abs.Spec.Resources {
		if len(r.Fields) != 1 {
			return nil, goflow.Options{}, fmt.Errorf("resource %s: generation supports exactly one selected field, got %d", r.GVK, len(r.Fields))
		}
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
		gvk, ok := x.resourceOf(obj.Type())
		if !ok {
			return true
		}
		r, ok := x.resource[gvk]
		if !ok {
			err = goflow.NewDiagnostic(l.Package.Fset, id, "local "+id.Name, fmt.Sprintf("has type %s, which the abstraction does not select", gvk))
			return false
		}
		x.locals[id.Name] = r
		v := x.localVariable(id.Name, r.Fields[0].Path)
		scope[v] = expr.Symbol{Type: r.Fields[0].Type, Quint: v}
		return true
	})
	if err != nil {
		return nil, goflow.Options{}, err
	}
	opts := goflow.Options{File: l.Path, Scope: scope, Returns: []goflow.Return{{Zero: true}, {Error: true}}}
	opts.Call = x.call
	opts.Field = x.field
	return x, opts, nil
}

// resourceOf maps a Go API type (or pointer to one) to its GVK: a type
// declared in k8s.io/api/<group>/<version> by its path (the core group is
// ""), any other type by the unique catalog entry of its name whose version
// is the name of its package.
func (x *extraction) resourceOf(t types.Type) (ir.GVK, bool) {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return ir.GVK{}, false
	}
	pkg := n.Obj().Pkg()
	if rest, ok := strings.CutPrefix(pkg.Path(), "k8s.io/api/"); ok {
		group, version := path.Split(rest)
		group = strings.TrimSuffix(group, "/")
		if group == "core" {
			group = ""
		}
		return ir.GVK{Group: group, Version: version, Kind: n.Obj().Name()}, true
	}
	var match []ir.GVK
	for _, e := range x.cat.Entries() {
		if e.GVK.Kind == n.Obj().Name() && e.GVK.Version == pkg.Name() {
			match = append(match, e.GVK)
		}
	}
	if len(match) != 1 {
		return ir.GVK{}, false
	}
	return match[0], true
}

// field maps a selected field of a Go object local to its model variable.
func (x *extraction) field(root string, fieldPath []string) (expr.Expression, bool) {
	r, ok := x.locals[root]
	if !ok || abstraction.PathKey(fieldPath) != abstraction.PathKey(r.Fields[0].Path) {
		return "", false
	}
	return expr.Expression(x.localVariable(root, fieldPath)), true
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
	f := r.Fields[0]
	value := abstraction.VariableName(r.GVK.Kind, object, f.Path)
	version := abstraction.VariableName(r.GVK.Kind, object, versionPath)
	b := map[string]string{
		"$exists":          abstraction.ExistsVariable(r.GVK.Kind, object),
		"$snapshotVersion": x.localVariable(local, versionPath),
	}
	switch {
	case e.Operation == ir.OpGet && e.Consistency == ir.ConsistencyCached:
		// Objects are neither created nor deleted while the model runs, so
		// the cache's view of existence is the API server's.
		b["$cache"], b["$cacheVersion"], b["$snapshot"] = adaptergen.CachedVariable(value), adaptergen.CachedVariable(version), x.localVariable(local, f.Path)
	case e.Operation == ir.OpGet:
		b["$object"], b["$resourceVersion"], b["$snapshot"] = value, version, x.localVariable(local, f.Path)
	case e.Operation == ir.OpUpdate:
		b["$object"], b["$resourceVersion"], b["$value"] = value, version, x.localVariable(local, f.Path)
	default:
		return goflow.Node{}, false, fmt.Errorf("%s: no bindings for operation %s", c, e.Operation)
	}
	return goflow.Node{Kind: e.Operation, Effect: e.Effect, Consistency: e.Consistency, Bindings: b}, true, nil
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
