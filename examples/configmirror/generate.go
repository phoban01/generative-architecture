package configmirror

import (
	"fmt"
	"go/ast"
	"go/types"
	"path"
	"sort"
	"strings"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
)

// This file derives the ConfigMirror model from its authored inputs. The
// declarations below are the only facts about the controller that are not
// read from its source, the Abstraction document or the summary library;
// each is stated by symbol, never by source text.

// clientPackage declares the controller-runtime client interfaces.
const clientPackage = "sigs.k8s.io/controller-runtime/pkg/client"

// clientEffect is what a call of one client method means: the effect it
// performs, its operation key in the summary library and its consistency.
// The consistency follows the supported injection pattern: Client is the
// manager's client, whose reads are served by the informer cache, and
// APIReader reads the API server directly.
type clientEffect struct {
	Effect, Operation, Consistency string
}

var reconcileEffects = map[goflow.Callee]clientEffect{
	{Package: clientPackage, Receiver: "Client", Method: "Get"}:    {"readSource", ir.OpGet, ir.ConsistencyCached},
	{Package: clientPackage, Receiver: "Reader", Method: "Get"}:    {"readTarget", ir.OpGet, ir.ConsistencyDirect},
	{Package: clientPackage, Receiver: "Client", Method: "Update"}: {"writeTarget", ir.OpUpdate, ir.ConsistencyDirect},
}

// keyObjects names, by field symbol, the abstraction object each key
// expression addresses: the reconcile request carries the source object's
// key (the controller is driven by that object), and the reconciler's
// Target field holds the copy's key.
var keyObjects = map[string]string{
	"sigs.k8s.io/controller-runtime/pkg/reconcile.Request.NamespacedName": "source",
	"generative-architecture/examples/configmirror.Reconciler.Target":     "copy",
}

// localFields keeps a selected field of a Go object local in a declared
// abstraction local instead of a generated one: the value read from the
// source is the snapshot the controller copies.
var localFields = map[string]string{"source.data.value": "snapshot"}

// versionPath is the JSON path of the object version the summaries compare.
var versionPath = []string{"metadata", "resourceVersion"}

// extraction holds the model names extraction binds Go objects to.
type extraction struct {
	abs      abstraction.Abstraction
	info     *types.Info
	body     *ast.BlockStmt
	locals   map[string]abstraction.Resource // Go object local -> its resource
	resource map[ir.GVK]abstraction.Resource
}

// localVariable is the model variable holding a selected field of a Go
// object local.
func localVariable(local string, fieldPath []string) string {
	if v, ok := localFields[local+"."+abstraction.PathKey(fieldPath)]; ok {
		return v
	}
	return local + "_" + strings.Join(fieldPath, "_")
}

// cacheVariable is the informer cache's copy of an object variable.
func cacheVariable(v string) string { return "cache_" + v }

// ExtractOptions builds the typed-frontend options for Reconcile: client
// calls resolve by callee symbol to their effects and summary bindings, and
// fields of object locals resolve by JSON path to model variables. It uses
// no statement callback and no source-text map.
func ExtractOptions(l goflow.Loaded, abs abstraction.Abstraction) (goflow.Options, error) {
	x := &extraction{abs: abs, info: l.Package.TypesInfo, body: l.Func.Body, locals: map[string]abstraction.Resource{}, resource: map[ir.GVK]abstraction.Resource{}}
	for _, r := range abs.Spec.Resources {
		if len(r.Fields) != 1 {
			return goflow.Options{}, fmt.Errorf("resource %s: generation supports exactly one selected field, got %d", r.GVK, len(r.Fields))
		}
		x.resource[r.GVK] = r
	}
	scope := map[string]expr.Symbol{}
	var err error
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
		gvk, ok := resourceOf(obj.Type())
		if !ok {
			return true
		}
		r, ok := x.resource[gvk]
		if !ok {
			err = fmt.Errorf("local %s has type %s, which the abstraction does not select", id.Name, gvk)
			return false
		}
		x.locals[id.Name] = r
		f := r.Fields[0]
		scope[localVariable(id.Name, f.Path)] = expr.Symbol{Type: f.Type, Quint: localVariable(id.Name, f.Path)}
		return true
	})
	if err != nil {
		return goflow.Options{}, err
	}
	opts := goflow.Options{File: l.Path, Scope: scope, Returns: []goflow.Return{{Zero: true}, {Error: true}}}
	opts.Call = x.call
	opts.Field = x.field
	return opts, nil
}

// resourceOf maps a Go API type (or pointer to one) declared in
// k8s.io/api/<group>/<version> to its GVK; the core group is "".
func resourceOf(t types.Type) (ir.GVK, bool) {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return ir.GVK{}, false
	}
	pkg := n.Obj().Pkg().Path()
	if !strings.HasPrefix(pkg, "k8s.io/api/") {
		return ir.GVK{}, false
	}
	group, version := path.Split(strings.TrimPrefix(pkg, "k8s.io/api/"))
	group = strings.TrimSuffix(group, "/")
	if group == "core" {
		group = ""
	}
	return ir.GVK{Group: group, Version: version, Kind: n.Obj().Name()}, true
}

// field maps a selected field of a Go object local to its model variable.
func (x *extraction) field(root string, fieldPath []string) (expr.Expression, bool) {
	r, ok := x.locals[root]
	if !ok || abstraction.PathKey(fieldPath) != abstraction.PathKey(r.Fields[0].Path) {
		return "", false
	}
	return expr.Expression(localVariable(root, fieldPath)), true
}

// call summarizes a client call: its effect, operation, consistency and
// the bindings of the summary placeholders to the addressed object's
// variables and the Go local the object is read into or written from.
func (x *extraction) call(c goflow.Callee, call *ast.CallExpr, _ goflow.Convert) (goflow.Node, bool, error) {
	e, ok := reconcileEffects[c]
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
	f := r.Fields[0]
	value := abstraction.VariableName(r.GVK.Kind, object, f.Path)
	version := abstraction.VariableName(r.GVK.Kind, object, versionPath)
	b := map[string]string{
		"$exists":          abstraction.ExistsVariable(r.GVK.Kind, object),
		"$snapshotVersion": localVariable(local, versionPath),
	}
	switch {
	case e.Operation == ir.OpGet && e.Consistency == ir.ConsistencyCached:
		// Objects are neither created nor deleted while the model runs, so
		// the cache's view of existence is the API server's.
		b["$cache"], b["$cacheVersion"], b["$snapshot"] = cacheVariable(value), cacheVariable(version), localVariable(local, f.Path)
	case e.Operation == ir.OpGet:
		b["$object"], b["$resourceVersion"], b["$snapshot"] = value, version, localVariable(local, f.Path)
	case e.Operation == ir.OpUpdate:
		b["$object"], b["$resourceVersion"], b["$value"] = value, version, localVariable(local, f.Path)
	default:
		return goflow.Node{}, false, fmt.Errorf("%s: no bindings for operation %s", c, e.Operation)
	}
	return goflow.Node{Kind: e.Operation, Effect: e.Effect, Consistency: e.Consistency, Bindings: b}, true, nil
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
	object, ok := keyObjects[symbol]
	if !ok {
		return "", fmt.Errorf("object key %s addresses no declared object", symbol)
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
		e, ok := reconcileEffects[goflow.Callee{Package: s.Obj().Pkg().Path(), Receiver: named.Obj().Name(), Method: s.Obj().Name()}]
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
