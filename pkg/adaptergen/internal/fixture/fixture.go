// Package fixture supplies the inputs the adaptergen tests generate from: the
// sample database abstraction and a catalog for its kinds. The generator
// tests compare the output to golden files; the generated package compiles
// and runs the same output against fake clients.
package fixture

import (
	"path/filepath"
	"runtime"

	"generative-architecture/pkg/abstraction"
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
