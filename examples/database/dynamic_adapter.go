package database

import (
	"fmt"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/effect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// Project the real fixture contents independently of the model and cached Store
// snapshots. Environment calls use the underlying fake API; DatabaseReconciler uses the observer.
func (a *Adapter) projectDynamic() (map[string]any, error) {
	state := map[string]any{"databaseExists": false, "contractSatisfied": false, "instanceExists": false, "statusEndpoint": "", "instanceEndpoint": ""}
	database, err := a.api.Resource(a.dynamicStore.DatabaseResource).Namespace(databaseKey.Namespace).Get(a.ctx, databaseKey.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		state["databaseExists"] = true
		state["contractSatisfied"] = database.GetAnnotations()["architecture.local/provisioning-contract"] == "v1"
		id, _, err := unstructured.NestedString(database.Object, "status", "endpoint")
		if err != nil {
			return nil, err
		}
		state["statusEndpoint"] = id
	}
	instance, err := a.api.Resource(a.dynamicStore.InstanceResource).Namespace(databaseKey.Namespace).Get(a.ctx, "instance-for-"+databaseKey.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		state["instanceExists"] = true
		id, _, err := unstructured.NestedString(instance.Object, "status", "endpoint")
		if err != nil {
			return nil, err
		}
		state["instanceEndpoint"] = id
		reference, _, err := unstructured.NestedString(instance.Object, "spec", "databaseName")
		if err != nil {
			return nil, err
		}
		if reference != databaseKey.Name {
			return nil, fmt.Errorf("instance points at the wrong Database")
		}
	}
	return state, nil
}
