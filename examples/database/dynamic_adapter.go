package database

import (
	"generative-architecture/examples/database/generated"
	"generative-architecture/pkg/abstraction"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/effect"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func (a *Adapter) initDynamic(e Example) error {
	mapper, err := e.Resources.Mapper()
	if err != nil {
		return err
	}
	database, err := e.Resources.Resolve("s.DatabaseResource")
	if err != nil {
		return err
	}
	instance, err := e.Resources.Resolve("s.InstanceResource")
	if err != nil {
		return err
	}
	registry, err := effect.NewRegistry(e.Spec.Effects)
	if err != nil {
		return err
	}
	// No provider types are registered: objects are stored and read unstructured.
	a.api = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	a.mapper = mapper
	a.dynamicStore = &DynamicStore{Client: cr.Observer{Registry: registry}.Dynamic(a.api, mapper), Context: a.ctx, Key: databaseKey, DatabaseResource: database.GVR(), InstanceResource: instance.GVR(), DatabaseKind: database.GVK(), InstanceKind: instance.GVK()}
	return nil
}

// Abstraction variables the schedules and the fixture operations address.
var (
	databaseExistsVariable   = abstraction.ExistsVariable("Database", "db1")
	databaseEndpointVariable = abstraction.VariableName("Database", "db1", []string{"status", "endpoint"})
	contractVariable         = abstraction.FieldVariable("Database", "db1", abstraction.Field{Name: "contractSatisfied"})
	instanceExistsVariable   = abstraction.ExistsVariable("DatabaseInstance", "inst1")
	instanceEndpointVariable = abstraction.VariableName("DatabaseInstance", "inst1", []string{"status", "endpoint"})
)

// projectAPI projects the real fixture contents with the projection
// generated from abstraction.yaml, independently of the model and cached
// Store snapshots. It reads the underlying fake API, not the observed client
// DatabaseReconciler uses, so projecting crosses no boundary gate. The
// projected names are the model's names. The generated reference check
// rejects an instance that names a Database the abstraction does not
// declare.
func (a *Adapter) projectAPI() (map[string]any, error) {
	if err := generated.CheckReferences(a.ctx, a.api, a.mapper); err != nil {
		return nil, err
	}
	return generated.Project(a.ctx, a.api, a.mapper)
}
