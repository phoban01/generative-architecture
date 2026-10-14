package sourcegen

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
)

// Generator names this frontend in provenance records.
const Generator = "sourcegen"

// reconcilerPackage declares the interface every controller implements.
const reconcilerPackage = "sigs.k8s.io/controller-runtime/pkg/reconcile"

// managerTypes are the accepted types of the one SetupWithManager parameter;
// ctrl.Manager is an alias of manager.Manager.
var managerTypes = map[string]bool{
	"sigs.k8s.io/controller-runtime.Manager":             true,
	"sigs.k8s.io/controller-runtime/pkg/manager.Manager": true,
}

// clientTypes give a reconciler access to the API. A reconciler must receive
// at least one of them through an exported field; see
// docs/supported-patterns.md.
var clientTypes = map[string]bool{
	"sigs.k8s.io/controller-runtime/pkg/client.Client": true,
	"sigs.k8s.io/controller-runtime/pkg/client.Reader": true,
	"k8s.io/client-go/dynamic.Interface":               true,
}

// injectableTypes are the dependency types recorded as injection, besides
// clientTypes and interfaces declared in the module under analysis.
var injectableTypes = map[string]bool{
	"k8s.io/client-go/tools/record.EventRecorder": true,
}

// managerGetters obtain clients from a manager. Called inside
// SetupWithManager they capture a client outside injection, which is not
// supported.
var managerGetters = map[string]bool{"GetClient": true, "GetAPIReader": true, "GetEventRecorderFor": true}

// Discovery is what Discover found: the controllers, and located
// diagnostics for construction patterns outside the supported set.
type Discovery struct {
	Controllers []ir.Controller
	Diagnostics []*goflow.Diagnostic
}

// DiscoverControllers discovers the reconcilers under root as Discover does
// and fails closed: when any construction pattern is unsupported, the
// returned error joins the diagnostics. The controllers found are returned
// alongside the error so a caller can report them.
func DiscoverControllers(root string, patterns []string) ([]ir.Controller, error) {
	d, err := Discover(root, patterns)
	if err != nil {
		return nil, err
	}
	if len(d.Diagnostics) == 0 {
		return d.Controllers, nil
	}
	errs := make([]error, 0, len(d.Diagnostics))
	for _, diag := range d.Diagnostics {
		errs = append(errs, diag)
	}
	return d.Controllers, errors.Join(errs...)
}

// Discover loads the local packages matched by patterns under root and
// returns every named struct type whose pointer implements
// reconcile.Reconciler, without requiring markers. A SetupWithManager method
// taking one controller-runtime Manager is recorded as the controller's
// setup, and its exported fields of a supported dependency type as its
// injection. The controller is named after its Go type; +arch:controller=
// <name> only overrides that name, which is needed when two packages declare
// reconcilers of the same type name.
//
// Patterns outside docs/supported-patterns.md are diagnosed rather than
// guessed at: a reconciler without an injected client, a package-level
// client variable, and a client obtained from the manager inside setup.
func Discover(root string, patterns []string) (Discovery, error) {
	var d Discovery
	abs, err := filepath.Abs(root)
	if err != nil {
		return d, err
	}
	root = abs
	if len(patterns) == 0 {
		return d, fmt.Errorf("source packages required")
	}
	for _, pattern := range patterns {
		if !strings.HasPrefix(pattern, "./") || strings.Contains(strings.TrimSuffix(pattern, "/..."), "..") {
			return d, fmt.Errorf("use explicit local package paths, got %q", pattern)
		}
	}
	pkgs, err := packages.Load(&packages.Config{Dir: root, Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps | packages.NeedModule}, patterns...)
	if err != nil {
		return d, err
	}
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			return d, fmt.Errorf("load %s: %s", p.PkgPath, p.Errors[0])
		}
		if p.TypesInfo == nil {
			return d, fmt.Errorf("load %s: no type information", p.PkgPath)
		}
	}
	iface := reconcilerInterface(pkgs)
	if iface == nil {
		// Nothing can implement an interface the module does not depend on.
		return d, nil
	}
	defs, err := definitions()
	if err != nil {
		return d, err
	}
	hashes := map[string]string{}
	names := map[string]string{}
	for _, p := range pkgs {
		module := ""
		if p.Module != nil {
			module = p.Module.Path
		}
		d.Diagnostics = append(d.Diagnostics, packageClients(root, p)...)
		for _, file := range p.Syntax {
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, spec := range gd.Specs {
					ts := spec.(*ast.TypeSpec)
					if ts.Assign.IsValid() || ts.TypeParams != nil {
						continue
					}
					obj, ok := p.TypesInfo.Defs[ts.Name].(*types.TypeName)
					if !ok {
						continue
					}
					n, ok := obj.Type().(*types.Named)
					if !ok {
						continue
					}
					if _, ok := n.Underlying().(*types.Struct); !ok || !types.Implements(types.NewPointer(n), iface) {
						continue
					}
					doc := ts.Doc
					if doc == nil && len(gd.Specs) == 1 {
						doc = gd.Doc
					}
					marks, err := parseComments(doc, defs, map[*ast.Comment]bool{})
					if err != nil {
						return d, fmt.Errorf("%s: %w", p.Fset.Position(ts.Pos()), err)
					}
					name := n.Obj().Name()
					if values := marks["arch:controller"]; len(values) > 0 {
						if len(values) != 1 || values[0].(string) == "" {
							return d, fmt.Errorf("%s: expected one non-empty +arch:controller marker", p.Fset.Position(ts.Pos()))
						}
						name = values[0].(string)
					}
					c := ir.Controller{ID: qualified(n), Name: name, Package: p.PkgPath, Receiver: n.Obj().Name(), Injection: injection(n, module)}
					if other, ok := names[name]; ok {
						return d, fmt.Errorf("controllers %s and %s share the name %q; add +arch:controller=<name> to one", other, c.ID, name)
					}
					names[name] = c.ID
					c.Provenance, err = provenance(root, p, ts.Pos(), hashes)
					if err != nil {
						return d, err
					}
					if !hasClient(c.Injection) {
						d.Diagnostics = append(d.Diagnostics, diagnostic(root, p, ts, "injection", fmt.Sprintf("reconciler %s receives no client: expected an exported field of type client.Client, client.Reader or dynamic.Interface", c.ID)))
					}
					var setup *ast.FuncDecl
					c.Setup, setup, err = setupMethod(root, p, n)
					if err != nil {
						return d, err
					}
					if setup != nil {
						d.Diagnostics = append(d.Diagnostics, setupClients(root, p, setup)...)
					}
					d.Controllers = append(d.Controllers, c)
				}
			}
		}
	}
	sort.Slice(d.Controllers, func(i, j int) bool { return d.Controllers[i].ID < d.Controllers[j].ID })
	sort.SliceStable(d.Diagnostics, func(i, j int) bool {
		a, b := d.Diagnostics[i], d.Diagnostics[j]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return d, nil
}

// reconcilerInterface finds reconcile.Reconciler among the loaded packages
// and their dependencies, or returns nil when the module does not depend on
// controller-runtime.
func reconcilerInterface(pkgs []*packages.Package) *types.Interface {
	var iface *types.Interface
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if iface != nil || p.PkgPath != reconcilerPackage || p.Types == nil {
			return
		}
		if obj, ok := p.Types.Scope().Lookup("Reconciler").(*types.TypeName); ok {
			iface, _ = obj.Type().Underlying().(*types.Interface)
		}
	})
	return iface
}

// injection lists the exported fields of n whose type is a supported
// dependency: a client type, an event recorder, or an interface declared in
// module. Other fields, including function-typed factories and concrete
// helpers, are not injection and are not recorded.
func injection(n *types.Named, module string) []ir.Field {
	st := n.Underlying().(*types.Struct)
	var out []ir.Field
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if !f.Exported() {
			continue
		}
		t := types.Unalias(f.Type())
		name := types.TypeString(t, nil)
		if clientTypes[name] || injectableTypes[name] || moduleInterface(t, module) {
			out = append(out, ir.Field{Name: f.Name(), Type: name})
		}
	}
	return out
}

// moduleInterface reports whether t is a named interface declared in module.
func moduleInterface(t types.Type, module string) bool {
	n, ok := t.(*types.Named)
	if !ok || module == "" || n.Obj().Pkg() == nil {
		return false
	}
	if _, ok := n.Underlying().(*types.Interface); !ok {
		return false
	}
	path := n.Obj().Pkg().Path()
	return path == module || strings.HasPrefix(path, module+"/")
}

// hasClient reports whether any injected field gives access to the API.
func hasClient(fields []ir.Field) bool {
	for _, f := range fields {
		if clientTypes[f.Type] {
			return true
		}
	}
	return false
}

// packageClients diagnoses package-level variables of a client type: a
// client that is not injected cannot be bounded or replaced in a model.
func packageClients(root string, p *packages.Package) []*goflow.Diagnostic {
	var out []*goflow.Diagnostic
	for _, file := range p.Syntax {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for _, id := range vs.Names {
					v, ok := p.TypesInfo.Defs[id].(*types.Var)
					if !ok {
						continue
					}
					t := types.Unalias(v.Type())
					if ptr, ok := t.(*types.Pointer); ok {
						t = types.Unalias(ptr.Elem())
					}
					if clientTypes[types.TypeString(t, nil)] {
						out = append(out, diagnostic(root, p, vs, "package-client", fmt.Sprintf("package-level client %s is not injected into a reconciler", id.Name)))
					}
				}
			}
		}
	}
	return out
}

// setupClients diagnoses clients obtained from the manager inside
// SetupWithManager, where they would be captured outside injection.
func setupClients(root string, p *packages.Package, fd *ast.FuncDecl) []*goflow.Diagnostic {
	if fd.Body == nil || len(fd.Type.Params.List) != 1 || len(fd.Type.Params.List[0].Names) != 1 {
		return nil
	}
	mgr := p.TypesInfo.Defs[fd.Type.Params.List[0].Names[0]]
	var out []*goflow.Diagnostic
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !managerGetters[sel.Sel.Name] {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && mgr != nil && p.TypesInfo.Uses[id] == mgr {
			out = append(out, diagnostic(root, p, call, "setup-client", "client obtained from the manager in setup is not injected into the reconciler"))
		}
		return true
	})
	return out
}

// diagnostic locates an unsupported construct relative to root.
func diagnostic(root string, p *packages.Package, n ast.Node, construct, reason string) *goflow.Diagnostic {
	d := goflow.NewDiagnostic(p.Fset, n, construct, reason)
	if rel, err := filepath.Rel(root, d.File); err == nil {
		d.File = filepath.ToSlash(rel)
	}
	return d
}

// provenance records the declaration at pos: its file relative to root, its
// line, the hash of the file and the module the package belongs to.
func provenance(root string, p *packages.Package, pos token.Pos, hashes map[string]string) (ir.Provenance, error) {
	loc := p.Fset.Position(pos)
	sum, ok := hashes[loc.Filename]
	if !ok {
		b, err := os.ReadFile(loc.Filename)
		if err != nil {
			return ir.Provenance{}, err
		}
		sum = Hash(b)
		hashes[loc.Filename] = sum
	}
	rel, err := filepath.Rel(root, loc.Filename)
	if err != nil {
		return ir.Provenance{}, err
	}
	prov := ir.Provenance{Generator: Generator, Source: ir.Source{File: filepath.ToSlash(rel), Line: loc.Line, SHA256: sum}}
	if p.Module != nil {
		prov.Module, prov.ModuleVersion = p.Module.Path, p.Module.Version
	}
	return prov, nil
}

// setupMethod locates SetupWithManager on the reconciler. The method must be
// declared on the type itself (a setup promoted from an embedded helper hides
// project-specific wiring) and take exactly one controller-runtime Manager; a
// method of that name with another signature is an error rather than a
// silently missing setup. The source hash covers the method declaration so
// receipts detect edits to the setup.
func setupMethod(root string, p *packages.Package, n *types.Named) (*ir.Source, *ast.FuncDecl, error) {
	obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(n), true, p.Types, "SetupWithManager")
	fn, ok := obj.(*types.Func)
	if !ok {
		return nil, nil, nil
	}
	sig := fn.Type().(*types.Signature)
	recv := sig.Recv().Type()
	if ptr, ok := recv.(*types.Pointer); ok {
		recv = ptr.Elem()
	}
	if !types.Identical(recv, n) {
		return nil, nil, fmt.Errorf("%s: SetupWithManager must be declared on %s, not promoted from an embedded type", p.Fset.Position(fn.Pos()), qualified(n))
	}
	if sig.Params().Len() != 1 || sig.Variadic() || !managerTypes[types.TypeString(types.Unalias(sig.Params().At(0).Type()), nil)] {
		return nil, nil, fmt.Errorf("%s: %s.SetupWithManager must take exactly one controller-runtime Manager", p.Fset.Position(fn.Pos()), qualified(n))
	}
	for _, file := range p.Syntax {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || p.TypesInfo.Defs[fd.Name] != fn {
				continue
			}
			loc := p.Fset.Position(fd.Pos())
			rel, err := filepath.Rel(root, loc.Filename)
			if err != nil {
				return nil, nil, err
			}
			return &ir.Source{File: filepath.ToSlash(rel), Line: loc.Line, SHA256: goflow.Hash([]byte(goflow.Code(fd)))}, fd, nil
		}
	}
	return nil, nil, fmt.Errorf("%s: SetupWithManager declaration of %s not found in its package", p.Fset.Position(fn.Pos()), qualified(n))
}
