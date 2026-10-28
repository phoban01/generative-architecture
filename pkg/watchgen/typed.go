package watchgen

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"os"

	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/goflow"
)

// ExtractLoaded reads the builder setup of a type-checked method. Package
// identifiers resolve through the package's type information, so imports
// need no alias and no fixed list: the identifier's types.PkgName object
// names the imported path. The supported builder syntax and the optional
// bindings for runtime GVKs are those of ExtractWithGVKs, which keeps the
// text-mode path for raw source.
func ExtractLoaded(l goflow.Loaded, resources, bindings map[string]effect.Resource) (Result, error) {
	if l.Package == nil || l.Package.TypesInfo == nil || l.Package.Fset == nil || l.File == nil {
		return Result{}, fmt.Errorf("ExtractLoaded requires a package with type information")
	}
	if l.Func == nil {
		return Result{}, fmt.Errorf("ExtractLoaded requires a method")
	}
	source, err := os.ReadFile(l.Path)
	if err != nil {
		return Result{}, err
	}
	info := l.Package.TypesInfo
	resolve := func(id *ast.Ident) (string, bool) {
		name, ok := info.Uses[id].(*types.PkgName)
		if !ok {
			return "", false
		}
		return name.Imported().Path(), true
	}
	receiver, method := "", l.Func.Name.Name
	if l.Func.Recv != nil && len(l.Func.Recv.List) == 1 {
		receiver = goflow.Code(l.Func.Recv.List[0].Type)
	}
	stringConstant := func(e ast.Expr) (string, bool) {
		tv, ok := info.Types[e]
		if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
			return "", false
		}
		return constant.StringVal(tv.Value), true
	}
	return extract(l.Package.Fset, l.File, l.Func, source, receiver, method, imports(l.File, info), resolve, stringConstant, resources, bindings)
}

// imports maps the package names in scope in f to their import paths, as
// type-checking resolved them, so a mapper can be rebuilt from its aliases.
func imports(f *ast.File, info *types.Info) map[string]string {
	out := map[string]string{}
	for _, spec := range f.Imports {
		var name *types.PkgName
		if spec.Name != nil {
			name, _ = info.Defs[spec.Name].(*types.PkgName)
		} else {
			name, _ = info.Implicits[spec].(*types.PkgName)
		}
		if name == nil || name.Name() == "." || name.Name() == "_" {
			continue
		}
		out[name.Name()] = name.Imported().Path()
	}
	return out
}
