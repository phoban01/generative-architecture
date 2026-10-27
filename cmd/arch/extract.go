package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"

	"generative-architecture/pkg/catalog"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/project"
	"generative-architecture/pkg/report"
	"generative-architecture/pkg/sourcegen"
	"generative-architecture/pkg/watchgen"
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
	result, err := watchgen.ExtractLoaded(l, watchedResources(l, cat), nil)
	if err != nil {
		return nil, err
	}
	return result.IR(c.ID, c.Setup.File), nil
}

// watchedResources maps each named type of a composite literal in the setup
// body to its catalog kind. A type matches when the catalog holds exactly
// one entry of the type's name whose version is the name of the type's
// package. Unmatched types are left out, so watchgen reports them as
// unknown watched resources with a location.
func watchedResources(l goflow.Loaded, cat *catalog.Catalog) map[string]effect.Resource {
	byKind := map[string][]ir.GVK{}
	for _, e := range cat.Entries() {
		byKind[e.GVK.Kind] = append(byKind[e.GVK.Kind], e.GVK)
	}
	out := map[string]effect.Resource{}
	ast.Inspect(l.Func.Body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		named, ok := types.Unalias(l.Package.TypesInfo.TypeOf(lit)).(*types.Named)
		if !ok || named.Obj().Pkg() == nil {
			return true
		}
		pkg := named.Obj().Pkg()
		var match []ir.GVK
		for _, gvk := range byKind[named.Obj().Name()] {
			if gvk.Version == pkg.Name() {
				match = append(match, gvk)
			}
		}
		if len(match) == 1 {
			out[pkg.Path()+"."+named.Obj().Name()] = effect.Resource{Group: match[0].Group, Version: match[0].Version, Kind: match[0].Kind}
		}
		return true
	})
	return out
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
