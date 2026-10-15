package database

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
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

// The abstraction declares exactly the API state the hand-written model
// observes: every model variable the adapter fills from the generated
// projection is a derived variable of the abstraction of the model's type,
// and every derived variable is either mapped or is the reference the
// adapter checks.
func TestAbstractionCoversObservedAPIState(t *testing.T) {
	a, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var base model.Spec
	if _, err := krm.Read("model.yaml", "TransitionModel", &base); err != nil {
		t.Fatal(err)
	}
	derived := a.StateVariables()
	used := map[string]bool{instanceReference: true}
	for model, projected := range projectedState {
		v, ok := derived[projected]
		if !ok {
			t.Fatalf("%s maps to %s, which the abstraction does not derive", model, projected)
		}
		if base.State[model].Type != v.Type {
			t.Fatalf("%s is %s in the model but %s projects as %s", model, base.State[model].Type, projected, v.Type)
		}
		used[projected] = true
	}
	for _, l := range a.Spec.Locals {
		if _, ok := base.State[l.Name]; !ok {
			t.Fatalf("local %s is not model state", l.Name)
		}
		used[l.Name] = true
	}
	var unused []string
	for name := range derived {
		if !used[name] {
			unused = append(unused, name)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Fatalf("abstraction derives variables the adapter never observes: %v", unused)
	}
	if _, ok := derived[instanceReference]; !ok {
		t.Fatalf("abstraction does not derive the reference %s", instanceReference)
	}
	r := a.Spec.Resources[1]
	if len(r.References) != 1 || abstraction.FieldVariable(r.GVK.Kind, r.Objects[0].ID, abstraction.Field{Path: r.References[0].Path}) != instanceReference {
		t.Fatalf("references %+v do not declare %s", r.References, instanceReference)
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
