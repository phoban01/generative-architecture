package sourcegen

import (
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

// DiscoverControllers loads the local packages matched by patterns under
// root and returns every named struct type whose pointer implements
// reconcile.Reconciler, without requiring markers. A SetupWithManager method
// taking one controller-runtime Manager is recorded as the controller's
// setup. The controller is named after its Go type; +arch:controller=<name>
// only overrides that name, which is needed when two packages declare
// reconcilers of the same type name.
func DiscoverControllers(root string, patterns []string) ([]ir.Controller, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = abs
	if len(patterns) == 0 {
		return nil, fmt.Errorf("source packages required")
	}
	for _, pattern := range patterns {
		if !strings.HasPrefix(pattern, "./") || strings.Contains(strings.TrimSuffix(pattern, "/..."), "..") {
			return nil, fmt.Errorf("use explicit local package paths, got %q", pattern)
		}
	}
	pkgs, err := packages.Load(&packages.Config{Dir: root, Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps | packages.NeedModule}, patterns...)
	if err != nil {
		return nil, err
	}
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			return nil, fmt.Errorf("load %s: %s", p.PkgPath, p.Errors[0])
		}
		if p.TypesInfo == nil {
			return nil, fmt.Errorf("load %s: no type information", p.PkgPath)
		}
	}
	iface := reconcilerInterface(pkgs)
	if iface == nil {
		// Nothing can implement an interface the module does not depend on.
		return nil, nil
	}
	defs, err := definitions()
	if err != nil {
		return nil, err
	}
	hashes := map[string]string{}
	var out []ir.Controller
	names := map[string]string{}
	for _, p := range pkgs {
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
						return nil, fmt.Errorf("%s: %w", p.Fset.Position(ts.Pos()), err)
					}
					name := n.Obj().Name()
					if values := marks["arch:controller"]; len(values) > 0 {
						if len(values) != 1 || values[0].(string) == "" {
							return nil, fmt.Errorf("%s: expected one non-empty +arch:controller marker", p.Fset.Position(ts.Pos()))
						}
						name = values[0].(string)
					}
					c := ir.Controller{ID: qualified(n), Name: name, Package: p.PkgPath, Receiver: n.Obj().Name()}
					if other, ok := names[name]; ok {
						return nil, fmt.Errorf("controllers %s and %s share the name %q; add +arch:controller=<name> to one", other, c.ID, name)
					}
					names[name] = c.ID
					c.Provenance, err = provenance(root, p, ts.Pos(), hashes)
					if err != nil {
						return nil, err
					}
					c.Setup, err = setupMethod(root, p, n)
					if err != nil {
						return nil, err
					}
					out = append(out, c)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
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
func setupMethod(root string, p *packages.Package, n *types.Named) (*ir.Source, error) {
	obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(n), true, p.Types, "SetupWithManager")
	fn, ok := obj.(*types.Func)
	if !ok {
		return nil, nil
	}
	sig := fn.Type().(*types.Signature)
	recv := sig.Recv().Type()
	if ptr, ok := recv.(*types.Pointer); ok {
		recv = ptr.Elem()
	}
	if !types.Identical(recv, n) {
		return nil, fmt.Errorf("%s: SetupWithManager must be declared on %s, not promoted from an embedded type", p.Fset.Position(fn.Pos()), qualified(n))
	}
	if sig.Params().Len() != 1 || sig.Variadic() || !managerTypes[types.TypeString(types.Unalias(sig.Params().At(0).Type()), nil)] {
		return nil, fmt.Errorf("%s: %s.SetupWithManager must take exactly one controller-runtime Manager", p.Fset.Position(fn.Pos()), qualified(n))
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
				return nil, err
			}
			return &ir.Source{File: filepath.ToSlash(rel), Line: loc.Line, SHA256: goflow.Hash([]byte(goflow.Code(fd)))}, nil
		}
	}
	return nil, fmt.Errorf("%s: SetupWithManager declaration of %s not found in its package", p.Fset.Position(fn.Pos()), qualified(n))
}
