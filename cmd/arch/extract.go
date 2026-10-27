package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/phoban01/generative-architecture/pkg/catalog"
	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/project"
	"github.com/phoban01/generative-architecture/pkg/report"
	"github.com/phoban01/generative-architecture/pkg/sourcegen"
	"github.com/phoban01/generative-architecture/pkg/watchgen"
)

// Output file names under the --out directory.
const (
	irFile      = "ir.yaml"
	supportFile = "support.yaml"
)

// errUnsupported marks an extraction that found unsupported constructs. The
// support report lists them; no IR is written.
var errUnsupported = errors.New("unsupported constructs")

// runExtract loads the project, builds its catalog, discovers controllers
// and extracts the watches of each SetupWithManager. It writes the IR to
// <out>/ir.yaml and the support report to <out>/support.yaml. When any
// construct is unsupported it writes only the support report, removes a
// stale IR and fails.
func runExtract(g globals, args []string) error {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("arch extract: unexpected arguments %v", fs.Args())
	}
	p, err := project.Load(g.project)
	if err != nil {
		return fmt.Errorf("arch extract: %w", err)
	}
	doc, support, err := extract(p)
	if err != nil {
		return fmt.Errorf("arch extract: %w", err)
	}
	support.Unresolved = append(support.Unresolved, unresolvedFacts(doc)...)
	if err := os.MkdirAll(g.out, 0o755); err != nil {
		return err
	}
	if err := support.Write(filepath.Join(g.out, supportFile)); err != nil {
		return err
	}
	irPath := filepath.Join(g.out, irFile)
	if n := len(support.Unsupported); n > 0 {
		if err := os.Remove(irPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return fmt.Errorf("arch extract: %w: %d found, listed in %s", errUnsupported, n, filepath.Join(g.out, supportFile))
	}
	if err := doc.Write(irPath, p.Name); err != nil {
		return err
	}
	if err := enforce(g, p, *support); err != nil {
		return fmt.Errorf("arch extract: %w", err)
	}
	return nil
}

// extract assembles the IR and the support report of a loaded project.
// Located diagnostics go to the report; other failures are returned.
func extract(p project.Project) (ir.Document, *report.Support, error) {
	doc := ir.Document{Schema: ir.SchemaVersion}
	support := &report.Support{Name: p.Name}
	cat, err := p.LoadCatalog()
	if err != nil {
		return doc, nil, err
	}
	d, err := sourcegen.Discover(p.Root, p.Spec.Packages)
	if err != nil {
		return doc, nil, err
	}
	for _, diag := range d.Diagnostics {
		support.Unsupported = append(support.Unsupported, diag.Item())
	}
	doc.Controllers = d.Controllers
	for _, c := range d.Controllers {
		loc := report.Location{File: c.Provenance.Source.File, Line: c.Provenance.Source.Line}
		support.Included = append(support.Included, report.Item{Kind: "controller", Construct: c.ID, Location: loc})
		if c.Setup == nil {
			support.Summarized = append(support.Summarized, report.Item{Kind: "setup", Construct: c.ID, Reason: "no SetupWithManager method; watches are not extracted", Location: loc})
			continue
		}
		watches, err := extractWatches(p.Root, c, cat)
		if d, ok := goflow.AsDiagnostic(err); ok {
			d.File = relative(p.Root, d.File)
			support.Unsupported = append(support.Unsupported, d.Item())
			continue
		}
		if err != nil {
			return doc, nil, fmt.Errorf("controller %s: %w", c.ID, err)
		}
		for _, w := range watches {
			support.Included = append(support.Included, report.Item{Kind: "watch", Construct: c.ID + " watches " + w.Resource.String(), Location: report.Location{File: w.Provenance.Source.File, Line: w.Provenance.Source.Line}})
		}
		doc.Watches = append(doc.Watches, watches...)
	}
	return doc, support, nil
}

// extractWatches type-checks the controller's SetupWithManager and converts
// its builder chain into IR watches.
func extractWatches(root string, c ir.Controller, cat *catalog.Catalog) ([]ir.Watch, error) {
	l, err := goflow.Load(root, goflow.Selector{Package: c.Package, Receiver: c.Receiver, Method: "SetupWithManager"})
	if err != nil {
		return nil, err
	}
	resources, err := watchedResources(l, cat)
	if err != nil {
		return nil, err
	}
	result, err := watchgen.ExtractLoaded(l, resources, nil)
	if err != nil {
		return nil, err
	}
	return result.IR(c.ID, c.Setup.File), nil
}

// watchedResources maps each named type of a composite literal in the setup
// file to its catalog kind. A list type such as WidgetList, which a map
// function lists, maps under the name of its item kind. The type's package
// gives the group and version (see groupVersion); a type matches the catalog
// entry with that group, version and the type's kind. Types whose kind no
// catalog entry has are left out, and so are types without a matching
// entry: watchgen reports them as unknown resources with a location. A type
// whose kind the catalog has, in a package that declares no group and
// version, fails with a located diagnostic.
func watchedResources(l goflow.Loaded, cat *catalog.Catalog) (map[string]effect.Resource, error) {
	kinds := map[string]bool{}
	for _, e := range cat.Entries() {
		kinds[e.GVK.Kind] = true
	}
	out := map[string]effect.Resource{}
	versions := map[string]groupVersionResult{}
	var failure error
	ast.Inspect(l.File, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || failure != nil {
			return failure == nil
		}
		named, ok := types.Unalias(l.Package.TypesInfo.TypeOf(lit)).(*types.Named)
		if !ok || named.Obj().Pkg() == nil {
			return true
		}
		pkg, kind := named.Obj().Pkg(), named.Obj().Name()
		if !kinds[kind] {
			kind = strings.TrimSuffix(kind, "List")
			if kind == named.Obj().Name() || !kinds[kind] {
				return true
			}
		}
		gv, seen := versions[pkg.Path()]
		if !seen {
			gv.group, gv.version, gv.ok = groupVersion(l.Package, pkg.Path())
			versions[pkg.Path()] = gv
		}
		if !gv.ok {
			failure = goflow.NewDiagnostic(l.Package.Fset, lit, "group-version", fmt.Sprintf("package %s declares no GroupVersion variable and no +groupName marker", pkg.Path()))
			return false
		}
		if e, ok := cat.Lookup(ir.GVK{Group: gv.group, Version: gv.version, Kind: kind}); ok {
			out[pkg.Path()+"."+kind] = effect.Resource{Group: e.GVK.Group, Version: e.GVK.Version, Kind: e.GVK.Kind}
		}
		return true
	})
	return out, failure
}

type groupVersionResult struct {
	group, version string
	ok             bool
}

// groupVersion finds the group and version of the package at path among the
// packages that from imports. A package-level GroupVersion or
// SchemeGroupVersion variable set to a schema.GroupVersion literal with
// constant fields gives both. Failing that, a +groupName=<group> marker in
// a package comment gives the group, and the package name is the version,
// as controller-gen reads it.
func groupVersion(from *packages.Package, path string) (group, version string, ok bool) {
	pkg := findPackage(from, path, map[string]bool{})
	if pkg == nil || pkg.TypesInfo == nil {
		return "", "", false
	}
	str := func(e ast.Expr) (string, bool) {
		tv, ok := pkg.TypesInfo.Types[e]
		if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
			return "", false
		}
		return constant.StringVal(tv.Value), true
	}
	for _, f := range pkg.Syntax {
		for _, d := range f.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				v := spec.(*ast.ValueSpec)
				for i, name := range v.Names {
					if (name.Name != "GroupVersion" && name.Name != "SchemeGroupVersion") || i >= len(v.Values) {
						continue
					}
					lit, ok := v.Values[i].(*ast.CompositeLit)
					if !ok || types.TypeString(pkg.TypesInfo.TypeOf(lit), nil) != "k8s.io/apimachinery/pkg/runtime/schema.GroupVersion" {
						continue
					}
					var gotGroup, gotVersion bool
					for _, el := range lit.Elts {
						kv, ok := el.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, _ := kv.Key.(*ast.Ident)
						s, isString := str(kv.Value)
						switch {
						case key == nil || !isString:
						case key.Name == "Group":
							group, gotGroup = s, true
						case key.Name == "Version":
							version, gotVersion = s, true
						}
					}
					if gotGroup && gotVersion && version != "" {
						return group, version, true
					}
				}
			}
		}
	}
	for _, f := range pkg.Syntax {
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
				if g, found := strings.CutPrefix(text, "+groupName="); found {
					return strings.TrimSpace(g), pkg.Name, true
				}
			}
		}
	}
	return "", "", false
}

// findPackage returns the package at path in the import graph of from.
func findPackage(from *packages.Package, path string, visited map[string]bool) *packages.Package {
	if from.PkgPath == path {
		return from
	}
	if visited[from.PkgPath] {
		return nil
	}
	visited[from.PkgPath] = true
	for _, imp := range from.Imports {
		if p := findPackage(imp, path, visited); p != nil {
			return p
		}
	}
	return nil
}

// relative makes an absolute diagnostic path relative to the module root.
func relative(root, path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}
