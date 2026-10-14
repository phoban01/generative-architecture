// Package fixture supplies the inputs the adaptergen tests generate from: the
// sample database abstraction, a catalog for its kinds and environment
// actions on its objects. The generator tests compare the output to golden
// files; the generated package compiles and runs the same output against
// fake clients.
package fixture

import (
	"path/filepath"
	"runtime"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/catalog"
	"generative-architecture/pkg/ir"
)

// Package is the package name the fixtures are generated into.
const Package = "generated"

// Kinds of the sample abstraction.
var (
	Database = ir.GVK{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "Database"}
	Instance = ir.GVK{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "DatabaseInstance"}
)

// AbstractionPath locates pkg/abstraction/testdata/database.yaml from this
// file, independent of the test working directory.
func AbstractionPath() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "abstraction", "testdata", "database.yaml")
}

// Abstraction loads the sample database abstraction.
func Abstraction() (abstraction.Abstraction, error) {
	return abstraction.Load(AbstractionPath())
}

// Catalog maps the sample kinds to namespaced plural resources.
func Catalog() (*catalog.Catalog, error) {
	c := catalog.New()
	for _, e := range []catalog.Entry{
		{GVK: Database, GVR: ir.GVR{Group: Database.Group, Version: Database.Version, Resource: "databases"}, Scope: ir.Namespaced, Served: true, Storage: true, Origin: "fixture"},
		{GVK: Instance, GVR: ir.GVR{Group: Instance.Group, Version: Instance.Version, Resource: "databaseinstances"}, Scope: ir.Namespaced, Served: true, Storage: true, Origin: "fixture"},
	} {
		if err := c.Add(e); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Actions are environment operations on the sample objects: the environment
// creates the Database and its instance, resizes the Database, publishes
// the instance endpoint through the status subresource and removes the
// Database again.
func Actions() []adaptergen.EnvAction {
	db := ir.ObjectRef{Namespace: "demo", Name: "db-1"}
	inst := ir.ObjectRef{Namespace: "demo", Name: "instance-for-db-1"}
	return []adaptergen.EnvAction{
		{Name: "discoverDatabase", Operation: adaptergen.ActionCreate, Resource: Database, Key: db, Fields: map[string]string{"spec.size": "small"}},
		{Name: "createInstance", Operation: adaptergen.ActionCreate, Resource: Instance, Key: inst, Fields: map[string]string{"spec.databaseName": "db-1"}},
		{Name: "resizeDatabase", Operation: adaptergen.ActionUpdate, Resource: Database, Key: db, Fields: map[string]string{"spec.size": "large"}},
		{Name: "provisionInstance", Operation: adaptergen.ActionStatus, Resource: Instance, Key: inst, Fields: map[string]string{"status.endpoint": "db-1.internal"}},
		{Name: "removeDatabase", Operation: adaptergen.ActionDelete, Resource: Database, Key: db},
	}
}
