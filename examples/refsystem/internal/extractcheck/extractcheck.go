// Package extractcheck runs the typed frontends over one reference-system
// controller package and collects the unsupported constructs they report.
// Controller tests use it to prove that each controller stays inside the
// subset the extractor supports today.
//
// The reference system has no abstraction document yet, so the options are
// deliberately permissive about data: every controller-runtime client call
// is a client operation and every field of basic type is a model symbol. A
// diagnostic therefore names a construct the frontends cannot read at all,
// not a field nobody selected.
package extractcheck

import (
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/sourcegen"
	"github.com/phoban01/generative-architecture/pkg/watchgen"
)

// clientPackage declares the controller-runtime client interfaces.
const clientPackage = "sigs.k8s.io/controller-runtime/pkg/client"

// resources are the Go types the reference system watches, keyed by
// qualified type name.
var resources = map[string]effect.Resource{
	"k8s.io/api/core/v1.Namespace":     {Version: "v1", Kind: "Namespace"},
	"k8s.io/api/core/v1.ResourceQuota": {Version: "v1", Kind: "ResourceQuota"},
	"k8s.io/api/core/v1.Secret":        {Version: "v1", Kind: "Secret"},
	"k8s.io/api/batch/v1.Job":          {Group: "batch", Version: "v1", Kind: "Job"},
	"github.com/phoban01/generative-architecture/examples/refsystem/api/v1alpha1.Tenant":   {Group: "refsystem.example.org", Version: "v1alpha1", Kind: "Tenant"},
	"github.com/phoban01/generative-architecture/examples/refsystem/api/v1alpha1.Database": {Group: "refsystem.example.org", Version: "v1alpha1", Kind: "Database"},
	"github.com/phoban01/generative-architecture/examples/refsystem/api/v1alpha1.Backup":   {Group: "refsystem.example.org", Version: "v1alpha1", Kind: "Backup"},
}

// Result is what the frontends read from one controller package.
type Result struct {
	Controllers []ir.Controller
	Program     *goflow.Program
	Watches     watchgen.Result
	// Diagnostics lists every unsupported construct, in frontend order:
	// discovery, then Reconcile, then SetupWithManager. goflow and watchgen
	// stop at their first diagnostic.
	Diagnostics []*goflow.Diagnostic
}

// Run discovers the reconciler in pkg (a module-relative pattern such as
// ./examples/refsystem/controllers/tenant), extracts its Reconcile method
// with goflow and its SetupWithManager method with watchgen. Diagnostics are
// collected; any other failure is returned as an error.
func Run(pkg, receiver string) (Result, error) {
	var out Result
	root, err := moduleRoot()
	if err != nil {
		return out, err
	}
	d, err := sourcegen.Discover(root, []string{pkg})
	if err != nil {
		return out, err
	}
	out.Controllers, out.Diagnostics = d.Controllers, d.Diagnostics

	l, err := goflow.Load(root, goflow.Selector{Package: pkg, Receiver: receiver, Method: "Reconcile"})
	if err != nil {
		return out, err
	}
	out.Program, err = goflow.ExtractTyped(l, options(l))
	if err := collect(&out, err); err != nil {
		return out, fmt.Errorf("Reconcile: %w", err)
	}

	l, err = goflow.Load(root, goflow.Selector{Package: pkg, Receiver: receiver, Method: "SetupWithManager"})
	if err != nil {
		return out, err
	}
	out.Watches, err = watchgen.ExtractLoaded(l, resources, nil)
	if err := collect(&out, err); err != nil {
		return out, fmt.Errorf("SetupWithManager: %w", err)
	}
	for _, diag := range out.Diagnostics {
		if rel, err := filepath.Rel(root, diag.File); err == nil && filepath.IsAbs(diag.File) {
			diag.File = filepath.ToSlash(rel)
		}
	}
	return out, nil
}

// collect records err as a diagnostic when it is one and returns it
// otherwise.
func collect(out *Result, err error) error {
	if err == nil {
		return nil
	}
	if d, ok := goflow.AsDiagnostic(err); ok {
		out.Diagnostics = append(out.Diagnostics, d)
		return nil
	}
	return err
}

// moduleRoot finds the directory holding go.mod above the working directory.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found")
		}
		dir = parent
	}
}

// options summarizes every client call by its method and maps every field
// of basic type on a local or parameter to a model symbol.
func options(l goflow.Loaded) goflow.Options {
	info := l.Package.TypesInfo
	scope := map[string]expr.Symbol{}
	// mapKeys lists the declared keys of each map field, so a literal
	// assigned above a map knows which of its keys are model symbols.
	mapKeys := map[string][]string{}
	declare := func(name string, t types.Type) {
		if typ, ok := modelType(t); ok {
			scope[name] = expr.Symbol{Type: typ, Quint: name}
		}
	}
	ast.Inspect(l.Func.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if root, path, ok := fieldPath(info, x); ok {
				declare(symbol(root, path), info.TypeOf(x))
			}
		case *ast.AssignStmt:
			// A map literal assigned to a field writes one symbol per key.
			if len(x.Lhs) != 1 || len(x.Rhs) != 1 {
				return true
			}
			sel, ok := ast.Unparen(x.Lhs[0]).(*ast.SelectorExpr)
			if !ok {
				return true
			}
			lit, ok := ast.Unparen(x.Rhs[0]).(*ast.CompositeLit)
			if !ok {
				return true
			}
			m, ok := types.Unalias(info.TypeOf(lit)).Underlying().(*types.Map)
			if !ok {
				return true
			}
			root, path, ok := fieldPath(info, sel)
			if !ok {
				return true
			}
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if tv, ok := info.Types[kv.Key]; ok && tv.Value != nil {
					key := strings.Trim(tv.Value.ExactString(), `"`)
					name := symbol(root, append(slices.Clone(path), key))
					declare(name, m.Elem())
					if _, ok := scope[name]; ok {
						mapKeys[symbol(root, path)] = append(mapKeys[symbol(root, path)], key)
					}
				}
			}
		}
		return true
	})
	opts := goflow.Options{File: l.Path, Scope: scope, Returns: []goflow.Return{{Zero: true}, {Error: true}}}
	opts.MapKeys = func(root string, path []string) []string {
		return mapKeys[symbol(root, path)]
	}
	opts.Call = func(c goflow.Callee, _ *ast.CallExpr, _ goflow.Convert) (goflow.Node, bool, error) {
		if c.Package != clientPackage {
			return goflow.Node{}, false, nil
		}
		op, ok := operations[c.Method]
		if !ok {
			return goflow.Node{}, false, nil
		}
		switch c.Receiver {
		case "Client", "Reader", "Writer":
			consistency := ir.ConsistencyDirect
			if c.Receiver == "Client" && (op == ir.OpGet || op == ir.OpList) {
				consistency = ir.ConsistencyCached
			}
			return goflow.Node{Kind: op, Effect: strings.ToLower(c.Receiver) + c.Method, Consistency: consistency}, true, nil
		case "SubResourceWriter":
			return goflow.Node{Kind: "status." + op, Effect: "status" + c.Method, Consistency: ir.ConsistencyDirect}, true, nil
		}
		return goflow.Node{}, false, nil
	}
	opts.Field = func(root string, path []string) (expr.Expression, bool) {
		name := symbol(root, path)
		_, ok := scope[name]
		return expr.Expression(name), ok
	}
	return opts
}

// operations maps client methods to their operation keys.
var operations = map[string]string{
	"Get":    ir.OpGet,
	"List":   ir.OpList,
	"Create": ir.OpCreate,
	"Update": ir.OpUpdate,
	"Patch":  ir.OpPatch,
	"Delete": ir.OpDelete,
}

// symbol names the model symbol of a field path on a local.
func symbol(root string, path []string) string {
	return root + "_" + strings.Join(path, "_")
}

// modelType maps a Go type of basic kind to its model type.
func modelType(t types.Type) (string, bool) {
	if t == nil {
		return "", false
	}
	b, ok := types.Unalias(t).Underlying().(*types.Basic)
	switch {
	case !ok:
		return "", false
	case b.Info()&types.IsString != 0:
		return "string", true
	case b.Info()&types.IsBoolean != 0:
		return "bool", true
	case b.Info()&types.IsInteger != 0:
		return "int", true
	}
	return "", false
}

// fieldPath returns the root identifier and JSON path of a chain of field
// selections, following goflow's rules: tag names, inlined embedded structs
// contribute nothing. It reports false for anything else.
func fieldPath(info *types.Info, x *ast.SelectorExpr) (string, []string, bool) {
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
	var path []string
	for i := len(hops) - 1; i >= 0; i-- {
		segment, ok := jsonPath(hops[i])
		if !ok {
			return "", nil, false
		}
		path = append(path, segment...)
	}
	return root.Name, path, len(path) > 0
}

// jsonPath returns the JSON names along one field selection.
func jsonPath(sel *types.Selection) ([]string, bool) {
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
