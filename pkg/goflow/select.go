package goflow

import (
	"fmt"
	"go/ast"
	"golang.org/x/tools/go/packages"
	"os"
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
}

// Load type-checks the package named by s (relative to root) and locates the
// selected method. The returned package keeps its type information so typed
// frontends can resolve identifiers, fields and callees by symbol.
func Load(root string, s Selector) (Loaded, error) {
	pkgs, err := packages.Load(&packages.Config{Dir: root, Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps | packages.NeedModule}, s.Package)
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
	for _, f := range p.Syntax {
		if fn := FindMethod(f, s.Receiver, s.Method); fn != nil {
			return Loaded{Package: p, File: f, Func: fn, Path: p.Fset.Position(fn.Pos()).Filename}, nil
		}
	}
	return Loaded{}, fmt.Errorf("method not found: %+v", s)
}

// Select loads the selected method and returns its source file and signature.
func Select(root string, s Selector) (Selection, error) {
	l, err := Load(root, s)
	if err != nil {
		return Selection{}, err
	}
	b, err := os.ReadFile(l.Path)
	if err != nil {
		return Selection{}, err
	}
	v := ""
	if l.Package.Module != nil {
		v = l.Package.Module.Version
	}
	return Selection{Source: b, File: l.Path, Signature: Code(l.Func.Type), Package: l.Package.PkgPath, ModuleVersion: v}, nil
}
