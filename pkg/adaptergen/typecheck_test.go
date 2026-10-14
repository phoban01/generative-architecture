package adaptergen_test

import (
	"flag"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

var update = flag.Bool("update", false, "rewrite golden files")

// typecheck parses and typechecks generated source as a package of this
// module, so its imports of the module and of client-go resolve exactly as
// they would for committed code. The file exists only in an overlay.
func typecheck(t *testing.T, src []byte) *packages.Package {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	const virtual = "pkg/adaptergen/internal/typecheck"
	cfg := &packages.Config{
		Dir:     root,
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Overlay: map[string][]byte{filepath.Join(root, filepath.FromSlash(virtual), "generated.go"): src},
	}
	pkgs, err := packages.Load(cfg, "./"+virtual)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected one package, got %d", len(pkgs))
	}
	p := pkgs[0]
	if len(p.Errors) > 0 {
		for _, e := range p.Errors {
			t.Errorf("generated source: %v", e)
		}
		t.Fatalf("generated source does not typecheck:\n%s", src)
	}
	if len(p.Syntax) != 1 || p.Types == nil {
		t.Fatalf("expected one typed file, got %d", len(p.Syntax))
	}
	return p
}

// signature returns the parameter and result type strings of a top-level
// function of the typechecked package.
func signature(t *testing.T, p *packages.Package, name string) (params, results []string) {
	t.Helper()
	obj := p.Types.Scope().Lookup(name)
	fn, ok := obj.(*types.Func)
	if !ok {
		t.Fatalf("generated source does not define func %s", name)
	}
	sig := fn.Type().(*types.Signature)
	for i := 0; i < sig.Params().Len(); i++ {
		params = append(params, sig.Params().At(i).Type().String())
	}
	for i := 0; i < sig.Results().Len(); i++ {
		results = append(results, sig.Results().At(i).Type().String())
	}
	return params, results
}

// assertNoModelState fails when generated code imports the packages that
// hold expected model state: a generated adapter observes the implementation
// only (ADOPT-3).
func assertNoModelState(t *testing.T, p *packages.Package) {
	t.Helper()
	for _, imp := range p.Syntax[0].Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		for _, forbidden := range []string{"generative-architecture/pkg/model", "generative-architecture/pkg/conformance", "generative-architecture/pkg/quint", "generative-architecture/pkg/expr"} {
			if path == forbidden {
				t.Errorf("generated source imports %s: adapters must not read expected model state", path)
			}
		}
	}
}

// golden compares src to testdata/<name>.golden, rewriting it under -update.
func golden(t *testing.T, name string, src []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(src) {
		t.Fatalf("%s differs from generated output; run go test ./pkg/adaptergen -update\n%s", path, src)
	}
}
