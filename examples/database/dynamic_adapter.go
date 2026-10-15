package database

import (
	"fmt"
	"generative-architecture/examples/database/generated"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/effect"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
func (a *Adapter) discoverDynamic() error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(a.dynamicStore.DatabaseKind)
	obj.SetName(databaseKey.Name)
	obj.SetNamespace(databaseKey.Namespace)
	obj.SetGeneration(1)
	if a.contractSatisfied {
		obj.SetAnnotations(map[string]string{"architecture.local/provisioning-contract": "v1"})
	}
	_, err := a.api.Resource(a.dynamicStore.DatabaseResource).Namespace(databaseKey.Namespace).Create(a.ctx, obj, metav1.CreateOptions{})
	return err
}
func (a *Adapter) provisionDynamic() error {
	resource := a.api.Resource(a.dynamicStore.InstanceResource).Namespace(databaseKey.Namespace)
	obj, err := resource.Get(a.ctx, "instance-for-"+databaseKey.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if err = unstructured.SetNestedField(obj.Object, "db-1.internal", "status", "endpoint"); err != nil {
		return err
	}
	_, err = resource.UpdateStatus(a.ctx, obj, metav1.UpdateOptions{})
	return err
}
func (a *Adapter) removeDynamic() error {
	return a.api.Resource(a.dynamicStore.DatabaseResource).Namespace(databaseKey.Namespace).Delete(a.ctx, databaseKey.Name, metav1.DeleteOptions{})
}

// projectedState names, for each API-state variable of the hand-written
// model, the variable abstraction.yaml derives for it and the generated
// projection reads.
var projectedState = map[string]string{
	"databaseExists":    "Database_db1_exists",
	"contractSatisfied": "Database_db1_contractSatisfied",
	"statusEndpoint":    "Database_db1_status_endpoint",
	"instanceExists":    "DatabaseInstance_inst1_exists",
	"instanceEndpoint":  "DatabaseInstance_inst1_status_endpoint",
}

// instanceReference is the instance's declared reference to its Database.
// The model has one Database, so the reference is checked, not modelled.
const instanceReference = "DatabaseInstance_inst1_spec_databaseName"

// projectAPI projects the real fixture contents with the projection
// generated from abstraction.yaml, independently of the model and cached
// Store snapshots. It reads the underlying fake API, not the observed client
// DatabaseReconciler uses, so projecting crosses no boundary gate.
func (a *Adapter) projectAPI() (map[string]any, error) {
	projected, err := generated.Project(a.ctx, a.api, a.mapper)
	if err != nil {
		return nil, err
	}
	if projected[projectedState["instanceExists"]] == true && projected[instanceReference] != databaseKey.Name {
		return nil, fmt.Errorf("instance points at the wrong Database: %v", projected[instanceReference])
	}
	state := make(map[string]any, len(projectedState))
	for model, name := range projectedState {
		v, ok := projected[name]
		if !ok {
			return nil, fmt.Errorf("generated projection lacks %s", name)
		}
		state[model] = v
	}
	return state, nil
}
