package goflow

import (
	"path/filepath"
	"testing"
)

// moduleRoot is the repository module root; tests load example packages
// from it with their own type information.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadReturnsTypedPackage(t *testing.T) {
	l, err := Load(moduleRoot(t), Selector{Package: "./examples/configmirror", Receiver: "Reconciler", Method: "Reconcile"})
	if err != nil {
		t.Fatal(err)
	}
	if l.Package == nil || l.Package.TypesInfo == nil {
		t.Fatalf("type information missing: %+v", l)
	}
	if l.Package.PkgPath != "generative-architecture/examples/configmirror" {
		t.Fatalf("unexpected package %q", l.Package.PkgPath)
	}
	if l.File == nil || l.Func == nil || l.Func.Name.Name != "Reconcile" {
		t.Fatalf("method not located: %+v", l)
	}
	if filepath.Base(l.Path) != "controller.go" {
		t.Fatalf("unexpected path %q", l.Path)
	}
	if obj := l.Package.TypesInfo.Defs[l.Func.Name]; obj == nil {
		t.Fatal("Reconcile has no definition in TypesInfo")
	}
	s, err := Select(moduleRoot(t), Selector{Package: "./examples/configmirror", Receiver: "Reconciler", Method: "Reconcile"})
	if err != nil {
		t.Fatal(err)
	}
	if s.File != l.Path || s.Package != l.Package.PkgPath || len(s.Source) == 0 {
		t.Fatalf("Select disagrees with Load: %+v", s)
	}
}
