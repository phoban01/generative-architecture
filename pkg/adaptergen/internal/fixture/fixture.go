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
	cr "generative-architecture/pkg/controllerruntime"
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

// Process is the control flow of the sample reconcile: read the Database,
// return when absent, read its instance, create one when missing, otherwise
// record the endpoint and return.
func Process() ir.Process {
	return ir.Process{
		ID:         "database-reconcile",
		Controller: "database",
		Method:     "Reconcile",
		Entry:      "n1",
		Nodes: []ir.Node{
			{ID: "n1", Kind: ir.NodeEffect, Effect: "readDatabase", Next: "n2"},
			{ID: "n2", Kind: ir.NodeBranch, Expression: "!Database_db1_exists", Next: "n6", Else: "n3"},
			{ID: "n3", Kind: ir.NodeEffect, Effect: "readInstance", Next: "n4"},
			{ID: "n4", Kind: ir.NodeBranch, Expression: "DatabaseInstance_inst1_exists", Next: "n5", Else: "n7"},
			{ID: "n5", Kind: ir.NodeAssign, Expression: "endpoint = DatabaseInstance_inst1_status_endpoint", Next: "n6"},
			{ID: "n6", Kind: ir.NodeReturn},
			{ID: "n7", Kind: ir.NodeEffect, Effect: "createInstance", Next: "n6"},
		},
	}
}

// Runtime is the group model of one FIFO controller reconciling the sample
// Database with one worker, retrying after two ticks, over a ten-tick clock.
func Runtime() (cr.GroupModel, error) {
	return cr.BuildGroupModel(cr.GroupProfile{Name: "DatabaseRuntime", MaxTicks: 10, Controllers: []cr.ModelController{{
		Name: "db", Workers: 1, RetryTicks: 2, AfterTicks: 2,
		Keys: []cr.ModelKey{{ID: "db1", Namespace: "demo", Name: "db-1"}},
	}}})
}

// RuntimeBinding names the runtime events and worker variables of Runtime
// as a composed model would: short names without the controller and key.
func RuntimeBinding() adaptergen.RuntimeBinding {
	return adaptergen.RuntimeBinding{
		Events:    map[string]string{"advance": "advance", "db_db1_enqueue": "enqueue", "db_db1_start": "begin", "db_db1_fire": "fire", "db_db1_success": "finish", "db_db1_error": "finishError"},
		Variables: map[string]string{"now": "now", "db_db1_ready": "ready", "db_db1_dirty": "dirty", "db_db1_processing": "processing", "db_db1_retries": "retries", "db_db1_due": "due"},
	}
}

// Nodes indexes the sample process by node ID, as ApplyProcessEvent takes it.
func Nodes() map[string]ir.Node {
	out := map[string]ir.Node{}
	for _, n := range Process().Nodes {
		out[n.ID] = n
	}
	return out
}
