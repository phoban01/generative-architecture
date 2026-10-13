package goflow

import (
	"fmt"
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

func Select(root string, s Selector) (Selection, error) {
	pkgs, err := packages.Load(&packages.Config{Dir: root, Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps | packages.NeedModule}, s.Package)
	if err != nil {
		return Selection{}, err
	}
	if len(pkgs) != 1 {
		return Selection{}, fmt.Errorf("selector must identify exactly one package")
	}
	p := pkgs[0]
	if len(p.Errors) > 0 {
		return Selection{}, fmt.Errorf("typecheck %s: %s", p.PkgPath, p.Errors[0])
	}
	for _, f := range p.Syntax {
		if fn := FindMethod(f, s.Receiver, s.Method); fn != nil {
			path := p.Fset.Position(fn.Pos()).Filename
			b, err := os.ReadFile(path)
			if err != nil {
				return Selection{}, err
			}
			v := ""
			if p.Module != nil {
				v = p.Module.Version
			}
			return Selection{Source: b, File: path, Signature: Code(fn.Type), Package: p.PkgPath, ModuleVersion: v}, nil
		}
	}
	return Selection{}, fmt.Errorf("method not found: %+v", s)
}
