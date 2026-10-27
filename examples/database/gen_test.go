package database

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/project"
)

// The replay adapter's API projection is generated from abstraction.yaml
// through the dynamic client. This test is the generator entry point: run
//
//	go test ./examples/database -run TestAdapterGeneratedFilesUpToDate -regenerate-adapter
//
// to re-emit generated/projection_generated.go; without the flag it fails
// when the committed file differs from what the generator produces.
var regenerateAdapter = flag.Bool("regenerate-adapter", false, "rewrite the generated replay adapter files")

// adapterProjection generates the dynamic-client projection of every
// object the abstraction declares.
func adapterProjection() ([]byte, error) {
	a, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		return nil, err
	}
	return adaptergen.GenerateProjection("generated", adaptergen.FromAbstraction(a))
}

func TestAdapterGeneratedFilesUpToDate(t *testing.T) {
	src, err := adapterProjection()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("generated", "projection_generated.go")
	if *regenerateAdapter {
		if err := os.WriteFile(path, src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	have, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(have) != string(src) {
		t.Fatalf("%s is stale; run go test ./examples/database -run TestAdapterGeneratedFilesUpToDate -regenerate-adapter", path)
	}
}

// The abstraction's kinds are the dynamic catalog's bindings, and project.yaml
// declares the abstraction's bounds.
func TestAbstractionMatchesCatalogAndProject(t *testing.T) {
	a, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		t.Fatal(err)
	}
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	var kinds, bound []ir.GVK
	for _, r := range a.Spec.Resources {
		kinds = append(kinds, r.GVK)
	}
	for _, b := range e.Resources.Bindings {
		bound = append(bound, ir.GVK{Group: b.Resource.Group, Version: b.Resource.Version, Kind: b.Resource.Kind})
	}
	if !reflect.DeepEqual(kinds, bound) {
		t.Fatalf("abstraction kinds %v, catalog bindings %v", kinds, bound)
	}
	p, err := project.Load("project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"maxObjectsPerResource": a.Spec.Bounds.MaxObjectsPerResource, "maxInvocations": a.Spec.Bounds.MaxInvocations}
	if !reflect.DeepEqual(p.Spec.Bounds, want) {
		t.Fatalf("project bounds %v, abstraction bounds %v", p.Spec.Bounds, want)
	}
	if !reflect.DeepEqual(p.Spec.Packages, []string{"./examples/database"}) || !reflect.DeepEqual(p.Spec.Constraints, []string{"constraints.yaml"}) {
		t.Fatalf("project %+v", p.Spec)
	}
}
