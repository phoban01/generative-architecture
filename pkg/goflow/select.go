package goflow

import (
	"fmt"
	"go/ast"
	"golang.org/x/tools/go/packages"
	"os"
	"path/filepath"
)

// Selector works with local or imported packages without requiring annotations.
type Selector struct {
	Package  string `yaml:"package"`
	Receiver string `yaml:"receiver"`
	Method   string `yaml:"method"`
}
type Selection struct {
	Source        []byte
	File          string
	Signature     string
	Package       string
	ModuleVersion string
}

// Loaded is a type-checked method: the package it belongs to (with TypesInfo
// populated), the file and declaration that define it, and the file's path.
type Loaded struct {
	Package *packages.Package
	File    *ast.File
	Func    *ast.FuncDecl
	Path    string
	// Source is the content of Path that was type-checked when an overlay
	// replaced the file. It is nil otherwise, and readers use the file on
	// disk.
	Source []byte
}

// LoadOptions configure LoadWith.
type LoadOptions struct {
	// Overlay maps a file path to the bytes that replace the file's
	// content. Nothing is written to disk. The map is passed to
	// packages.Config.Overlay; a relative path is relative to the load
	// root. Each path must name a Go file of the loaded packages.
	Overlay map[string][]byte
}

// Load type-checks the package named by s (relative to root) and locates the
// selected method. The returned package keeps its type information so typed
// frontends can resolve identifiers, fields and callees by symbol.
func Load(root string, s Selector) (Loaded, error) {
	return LoadWith(root, s, LoadOptions{})
}

// LoadWith is Load with options. With an overlay, the package is
// type-checked and the method located in the replaced source, so a mutated
// file rebuilds the program without being written to disk.
func LoadWith(root string, s Selector, opts LoadOptions) (Loaded, error) {
	overlay, err := absoluteOverlay(root, opts.Overlay)
	if err != nil {
		return Loaded{}, err
	}
	pkgs, err := packages.Load(&packages.Config{Dir: root, Overlay: overlay, Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps | packages.NeedModule}, s.Package)
	if err != nil {
		return Loaded{}, err
	}
	if len(pkgs) != 1 {
		return Loaded{}, fmt.Errorf("selector must identify exactly one package")
	}
	p := pkgs[0]
	if len(p.Errors) > 0 {
		return Loaded{}, fmt.Errorf("typecheck %s: %s", p.PkgPath, p.Errors[0])
	}
	if p.TypesInfo == nil {
		return Loaded{}, fmt.Errorf("typecheck %s: no type information", p.PkgPath)
	}
	if err = overlayUsed(pkgs, overlay); err != nil {
		return Loaded{}, err
	}
	for _, f := range p.Syntax {
		if fn := FindMethod(f, s.Receiver, s.Method); fn != nil {
			path := p.Fset.Position(fn.Pos()).Filename
			return Loaded{Package: p, File: f, Func: fn, Path: path, Source: overlay[path]}, nil
		}
	}
	return Loaded{}, fmt.Errorf("method not found: %+v", s)
}

// absoluteOverlay keys an overlay by clean absolute path, the form
// go/packages and token positions use. Two keys for one file are an error.
func absoluteOverlay(root string, overlay map[string][]byte) (map[string][]byte, error) {
	if len(overlay) == 0 {
		return nil, nil
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(overlay))
	for path, b := range overlay {
		if path == "" {
			return nil, fmt.Errorf("overlay: empty file path")
		}
		abs := filepath.Clean(path)
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(base, abs)
		}
		if _, dup := out[abs]; dup {
			return nil, fmt.Errorf("overlay: %s is replaced twice", abs)
		}
		out[abs] = b
	}
	return out, nil
}

// overlayUsed fails when an overlay file is not a Go file of a loaded
// package or its dependencies: the replacement would be silently ignored,
// for example because the path names another directory or a symlink.
func overlayUsed(pkgs []*packages.Package, overlay map[string][]byte) error {
	if len(overlay) == 0 {
		return nil
	}
	files := map[string]bool{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, f := range p.GoFiles {
			files[f] = true
		}
	})
	for path := range overlay {
		if !files[path] {
			return fmt.Errorf("overlay: %s is not a Go file of the loaded packages", path)
		}
	}
	return nil
}

// source returns the type-checked content of l.Path.
func (l Loaded) source() ([]byte, error) {
	if l.Source != nil {
		return l.Source, nil
	}
	return os.ReadFile(l.Path)
}

// Select loads the selected method and returns its source file and signature.
func Select(root string, s Selector) (Selection, error) {
	l, err := Load(root, s)
	if err != nil {
		return Selection{}, err
	}
	b, err := l.source()
	if err != nil {
		return Selection{}, err
	}
	v := ""
	if l.Package.Module != nil {
		v = l.Package.Module.Version
	}
	return Selection{Source: b, File: l.Path, Signature: Code(l.Func.Type), Package: l.Package.PkgPath, ModuleVersion: v}, nil
}
