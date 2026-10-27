package database

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/project"
)

// The replay adapter's projection, reference check, environment, process
// gating and Store proxy are generated from abstraction.yaml, the extracted
// process and the model's effects and environment actions. This test is
// the generator entry point: run
//
//	go test ./examples/database -run TestAdapterGeneratedFilesUpToDate -regenerate-adapter
//
// to re-emit the *_generated.go files; without the flag it fails when a
// committed file differs from what the generators produce.
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

// adapterReferences generates the check of every reference the abstraction
// declares.
func adapterReferences() ([]byte, error) {
	a, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		return nil, err
	}
	return adaptergen.GenerateReferenceCheck("generated", a.Spec)
}

// adapterEnvironment generates the environment from the model's
// environment actions: each sets one abstraction field of one object, and
// removeDatabaseBeforeRead deletes the Database.
func adapterEnvironment() ([]byte, error) {
	a, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		return nil, err
	}
	e, err := Load(".")
	if err != nil {
		return nil, err
	}
	var actions []adaptergen.EnvAction
	for _, env := range e.Environment {
		op := adaptergen.ActionCreate
		if env.Operation == EnvUpdate {
			op = adaptergen.ActionUpdate
		}
		action, err := adaptergen.FieldAction(a.Spec, env.Name, op, env.Object, env.Variable, env.Value)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	database := gvk("Database")
	actions = append(actions, adaptergen.EnvAction{Name: removeBeforeRead, Operation: adaptergen.ActionDelete, Resource: database, Key: ir.ObjectRef{GVK: database, Namespace: databaseKey.Namespace, Name: databaseKey.Name}})
	return adaptergen.GenerateEnvironment("generated", actions)
}

// adapterGating generates the gating of the extracted process, its nodes
// named by the model's process events.
func adapterGating() ([]byte, error) {
	e, err := Load(".")
	if err != nil {
		return nil, err
	}
	return adaptergen.GenerateGating("generated", e.ReconcileProcess)
}

// storeType typechecks controller.go alone for the Store interface.
func storeType() (*types.Named, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "controller.go", nil, 0)
	if err != nil {
		return nil, err
	}
	pkg, err := (&types.Config{}).Check("generative-architecture/examples/database", fset, []*ast.File{f}, nil)
	if err != nil {
		return nil, err
	}
	named, ok := pkg.Scope().Lookup("Store").Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("Store is not a named type")
	}
	return named, nil
}

// storeProxy generates the gating proxy of the Store interface, in this
// package: each method pauses at the gate with the effect the model
// declares for it.
func storeProxy() ([]byte, error) {
	e, err := Load(".")
	if err != nil {
		return nil, err
	}
	store, err := storeType()
	if err != nil {
		return nil, err
	}
	descriptors := map[string]effect.Descriptor{}
	for _, d := range e.Spec.Effects {
		descriptors[d.ID] = d
	}
	effects := map[string]ir.Effect{}
	for method, id := range storeEffects {
		d, ok := descriptors[id]
		if !ok {
			return nil, fmt.Errorf("Store method %s performs undeclared effect %s", method, id)
		}
		effects[method] = ir.Effect{ID: id, Kind: ir.KindAPI, Actor: d.Actor, Resource: ir.GVK{Group: d.Resource.Group, Version: d.Resource.Version, Kind: d.Resource.Kind}, Operation: d.Operation, Consistency: d.Consistency}
	}
	return adaptergen.GenerateInterfaceProxy("database", store, effects)
}

func TestAdapterGeneratedFilesUpToDate(t *testing.T) {
	for file, generate := range map[string]func() ([]byte, error){
		"generated/projection_generated.go":  adapterProjection,
		"generated/references_generated.go":  adapterReferences,
		"generated/environment_generated.go": adapterEnvironment,
		"generated/gating_generated.go":      adapterGating,
		"store_proxy_generated.go":           storeProxy,
	} {
		t.Run(file, func(t *testing.T) {
			src, err := generate()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.FromSlash(file)
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
		})
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
