package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/catalog"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// This file reads a reconciler's Reconcile method for arch extract and
// arch support (ADR-0008). It runs the typed frontend with the built-in
// summaries and needs no abstraction and no controller declaration, so
// the support report lists every construct the frontend cannot read
// before the author writes either document. It binds no model variable;
// arch generate extracts again with the bindings.

// clientPackage declares the controller-runtime client interfaces.
const clientPackage = "sigs.k8s.io/controller-runtime/pkg/client"

// clientOperations maps every client method to its operation key. The
// inventory reads them all; arch generate binds only some.
var clientOperations = map[string]string{
	"Get":    ir.OpGet,
	"List":   ir.OpList,
	"Create": ir.OpCreate,
	"Update": ir.OpUpdate,
	"Patch":  ir.OpPatch,
	"Delete": ir.OpDelete,
}

// inventory holds what a scan of the Reconcile body found: a model symbol
// for every object field of basic type, the map keys the body names and
// where each field first appears.
type inventory struct {
	fset  *token.FileSet
	file  string // relative to the module root
	scope map[string]expr.Symbol
	// keys lists the map keys the body names under a map field.
	keys map[string][]string
	// first is the first appearance of each field symbol, and names its
	// name as root.json.path.
	first map[string]token.Pos
	names map[string]string
	// selected marks the symbols the abstraction selects. Nil without an
	// abstraction.
	selected map[string]bool
	// unresolved collects the fields the frontend lowered without a
	// selection, by symbol.
	unresolved map[string]bool
}

// extractReconcile runs the typed frontend over the Reconcile method of l
// (loaded for c). The program is nil when the frontend reports a
// diagnostic. Diagnostics, and failures the frontend cannot locate, are
// returned as unsupported items; fields lowered without a selection are
// returned as unresolved items.
//
// Every object field of basic type that the body reads or writes is a
// model symbol. Without an abstraction each of them is unresolved, and so
// is each field a node writes without a model value. With an abstraction
// the fields it selects on a local of an abstracted resource resolve, and
// the map keys it selects complete each map literal; any other field is
// unresolved.
func extractReconcile(root string, c ir.Controller, l goflow.Loaded, abs *abstraction.Abstraction, cat *catalog.Catalog) (*goflow.Program, []report.Item, []report.Item) {
	inv := &inventory{
		fset: l.Package.Fset, file: relative(root, l.Path),
		scope: map[string]expr.Symbol{}, keys: map[string][]string{},
		first: map[string]token.Pos{}, names: map[string]string{}, unresolved: map[string]bool{},
	}
	inv.scan(l.Package.TypesInfo, l.Func.Body)
	if abs != nil {
		inv.selectFields(l.Package.TypesInfo, l.Func.Body, *abs, cat)
	}
	opts := goflow.Options{
		File:    l.Path,
		Scope:   inv.scope,
		Returns: []goflow.Return{{Zero: true, Requeue: true}, {Error: true}},
		Call:    clientCall,
		Field:   inv.field,
		MapKeys: inv.mapKeys,
	}
	program, err := goflow.ExtractTyped(l, opts)
	if err != nil {
		if ds, ok := goflow.AsDiagnostics(err); ok {
			var out []report.Item
			for _, d := range ds {
				d.File = relative(root, d.File)
				out = append(out, d.Item())
			}
			return nil, out, inv.items(nil)
		}
		at := l.Package.Fset.Position(l.Func.Pos())
		return nil, []report.Item{{Kind: "reconcile", Construct: c.ID, Reason: err.Error(), Location: report.Location{File: relative(root, at.Filename), Line: at.Line}}}, nil
	}
	return program, nil, inv.items(program)
}

// clientCall summarizes a call of a controller-runtime client method by
// its operation, with no bindings. A method promoted through a client
// embedded in the reconciler has the reconciler as its receiver; it is
// read as a call through client.Client, which the kubebuilder scaffold
// embeds.
func clientCall(c goflow.Callee, _ *ast.CallExpr, _ goflow.Convert) (goflow.Node, bool, error) {
	if c.Package != clientPackage || c.Receiver == "" {
		return goflow.Node{}, false, nil
	}
	op, ok := clientOperations[c.Method]
	if !ok {
		return goflow.Node{}, false, nil
	}
	switch c.Receiver {
	case "SubResourceWriter", "StatusWriter":
		return goflow.Node{Kind: "status." + op, Effect: "status" + c.Method, Consistency: ir.ConsistencyDirect}, true, nil
	case "Reader", "Writer":
		return goflow.Node{Kind: op, Effect: strings.ToLower(c.Receiver) + c.Method, Consistency: ir.ConsistencyDirect}, true, nil
	}
	consistency := ir.ConsistencyDirect
	if op == ir.OpGet || op == ir.OpList {
		consistency = ir.ConsistencyCached
	}
	return goflow.Node{Kind: op, Effect: "client" + c.Method, Consistency: consistency}, true, nil
}

// symbolOf names the model symbol of a field path on a variable.
func symbolOf(root string, path []string) string {
	return root + "_" + strings.Join(path, "_")
}

// declare records a field of the body: a symbol when its type is basic,
// and where it first appears.
func (inv *inventory) declare(root string, path []string, t types.Type, at token.Pos) {
	typ, ok := basicModelType(t)
	if !ok {
		return
	}
	name := symbolOf(root, path)
	if _, seen := inv.scope[name]; !seen {
		inv.scope[name] = expr.Symbol{Type: typ, Quint: name}
		inv.first[name] = at
		inv.names[name] = root + "." + strings.Join(path, ".")
	}
}

// scan declares every field the body selects, every map entry it indexes
// by a constant key and every key of a map literal it assigns to a field.
func (inv *inventory) scan(info *types.Info, body *ast.BlockStmt) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if root, path, ok := selection(info, x); ok {
				inv.declare(root, path, info.TypeOf(x), x.Pos())
			}
		case *ast.IndexExpr:
			sel, ok := ast.Unparen(x.X).(*ast.SelectorExpr)
			if !ok {
				return true
			}
			m, ok := types.Unalias(info.TypeOf(sel)).Underlying().(*types.Map)
			tv, isConst := info.Types[x.Index]
			if !ok || !isConst || tv.Value == nil {
				return true
			}
			if root, path, ok := selection(info, sel); ok {
				key := strings.Trim(tv.Value.ExactString(), `"`)
				inv.declare(root, append(slices.Clone(path), key), m.Elem(), x.Pos())
			}
		case *ast.AssignStmt:
			if len(x.Lhs) != 1 || len(x.Rhs) != 1 {
				return true
			}
			sel, ok := ast.Unparen(x.Lhs[0]).(*ast.SelectorExpr)
			lit, isLit := ast.Unparen(x.Rhs[0]).(*ast.CompositeLit)
			if !ok || !isLit {
				return true
			}
			m, ok := types.Unalias(info.TypeOf(lit)).Underlying().(*types.Map)
			root, path, isField := selection(info, sel)
			if !ok || !isField {
				return true
			}
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				tv, ok := info.Types[kv.Key]
				if !ok || tv.Value == nil {
					continue
				}
				key := strings.Trim(tv.Value.ExactString(), `"`)
				name := symbolOf(root, append(slices.Clone(path), key))
				inv.declare(root, append(slices.Clone(path), key), m.Elem(), kv.Pos())
				if _, ok := inv.scope[name]; ok && !slices.Contains(inv.keys[symbolOf(root, path)], key) {
					inv.keys[symbolOf(root, path)] = append(inv.keys[symbolOf(root, path)], key)
				}
			}
		}
		return true
	})
}

// selectFields marks the fields the abstraction selects on each local
// whose type is an abstracted resource, and declares them with the
// abstraction's types.
func (inv *inventory) selectFields(info *types.Info, body *ast.BlockStmt, abs abstraction.Abstraction, cat *catalog.Catalog) {
	inv.selected = map[string]bool{}
	resources := map[ir.GVK]abstraction.Resource{}
	for _, r := range abs.Spec.Resources {
		resources[r.GVK] = r
	}
	x := &extraction{cat: cat}
	ast.Inspect(body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		v, ok := info.Defs[id].(*types.Var)
		if !ok {
			return true
		}
		gvk, ok := x.resourceOf(v.Type())
		if !ok {
			return true
		}
		r, ok := resources[gvk]
		if !ok {
			return true
		}
		for _, f := range r.Fields {
			path := abstraction.JSONPath(f.Path)
			name := symbolOf(id.Name, path)
			if _, seen := inv.scope[name]; !seen {
				inv.scope[name] = expr.Symbol{Type: f.Type, Quint: name}
			}
			inv.selected[name] = true
			if len(path) > 1 {
				parent := symbolOf(id.Name, path[:len(path)-1])
				if !slices.Contains(inv.keys[parent], path[len(path)-1]) {
					inv.keys[parent] = append(inv.keys[parent], path[len(path)-1])
				}
			}
		}
		return true
	})
}

// field maps every declared field to its symbol. A field the abstraction
// does not select is unresolved; without an abstraction, every field is.
func (inv *inventory) field(root string, path []string) (expr.Expression, bool) {
	name := symbolOf(root, path)
	if _, ok := inv.scope[name]; !ok {
		return "", false
	}
	if !inv.selected[name] {
		inv.unresolved[name] = true
	}
	return expr.Expression(name), true
}

// mapKeys lists the keys of a map field that the body names or the
// abstraction selects.
func (inv *inventory) mapKeys(root string, path []string) []string {
	return inv.keys[symbolOf(root, path)]
}

// items lists the unresolved fields, one per symbol at its first
// appearance. Without an abstraction, the fields a node of the program
// writes without a model value are unresolved too.
func (inv *inventory) items(program *goflow.Program) []report.Item {
	reason := "no abstraction selects this field"
	if inv.selected != nil {
		reason = "the abstraction does not select this field"
	}
	var out []report.Item
	for name := range inv.unresolved {
		out = append(out, report.Item{Kind: "field", Construct: inv.names[name], Reason: reason, Location: inv.at(inv.first[name])})
	}
	if program != nil && inv.selected == nil {
		seen := map[string]bool{}
		for _, n := range program.Nodes {
			for _, f := range n.Unmodelled {
				if !seen[f] {
					seen[f] = true
					out = append(out, report.Item{Kind: "field", Construct: f, Reason: "written without a model value; " + reason, Location: report.Location{File: inv.file, Line: n.Line}})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Location.Line != out[j].Location.Line {
			return out[i].Location.Line < out[j].Location.Line
		}
		return out[i].Construct < out[j].Construct
	})
	return out
}

func (inv *inventory) at(pos token.Pos) report.Location {
	return report.Location{File: inv.file, Line: inv.fset.Position(pos).Line}
}

// basicModelType maps a Go type of basic kind to its model type.
func basicModelType(t types.Type) (string, bool) {
	if t == nil {
		return "", false
	}
	b, ok := types.Unalias(t).Underlying().(*types.Basic)
	switch {
	case !ok:
		return "", false
	case b.Info()&types.IsString != 0:
		return abstraction.TypeString, true
	case b.Info()&types.IsBoolean != 0:
		return abstraction.TypeBool, true
	case b.Info()&types.IsInteger != 0:
		return abstraction.TypeInt, true
	}
	return "", false
}

// selection returns the root variable and JSON path of a chain of field
// selections, following goflow's rules: tag names, and inlined embedded
// structs contribute nothing. It reports false for anything else.
func selection(info *types.Info, x *ast.SelectorExpr) (string, []string, bool) {
	var hops []*types.Selection
	var n ast.Expr = x
	for {
		s, ok := ast.Unparen(n).(*ast.SelectorExpr)
		if !ok {
			break
		}
		sel, ok := info.Selections[s]
		if !ok || sel.Kind() != types.FieldVal {
			return "", nil, false
		}
		hops = append(hops, sel)
		n = s.X
	}
	root, ok := ast.Unparen(n).(*ast.Ident)
	if !ok {
		return "", nil, false
	}
	if _, ok := info.Uses[root].(*types.Var); !ok {
		return "", nil, false
	}
	var path []string
	for i := len(hops) - 1; i >= 0; i-- {
		segment, ok := selectionPath(hops[i])
		if !ok {
			return "", nil, false
		}
		path = append(path, segment...)
	}
	return root.Name, path, len(path) > 0
}

// selectionPath returns the JSON names along one field selection.
func selectionPath(sel *types.Selection) ([]string, bool) {
	t := sel.Recv()
	var path []string
	for _, i := range sel.Index() {
		if p, ok := types.Unalias(t).(*types.Pointer); ok {
			t = p.Elem()
		}
		st, ok := t.Underlying().(*types.Struct)
		if !ok {
			return nil, false
		}
		f := st.Field(i)
		if !f.Exported() {
			return nil, false
		}
		tag := strings.Split(reflect.StructTag(st.Tag(i)).Get("json"), ",")
		switch name := tag[0]; {
		case name == "-":
			return nil, false
		case name == "" && (f.Embedded() || slices.Contains(tag[1:], "inline")):
		case name == "":
			path = append(path, f.Name())
		default:
			path = append(path, name)
		}
		t = f.Type()
	}
	return path, true
}

// sibling finds another method of the receiver in the package of l, so
// one load serves Reconcile and SetupWithManager.
func sibling(l goflow.Loaded, receiver, method string) (goflow.Loaded, bool) {
	for _, f := range l.Package.Syntax {
		if fn := goflow.FindMethod(f, receiver, method); fn != nil {
			return goflow.Loaded{Package: l.Package, File: f, Func: fn, Path: l.Package.Fset.Position(fn.Pos()).Filename}, true
		}
	}
	return goflow.Loaded{}, false
}
