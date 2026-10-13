package assignment

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
	infra, err := e.Resources.Resolve("s.InfraResource")
	if err != nil {
		return err
	}
	claim, err := e.Resources.Resolve("s.ClaimResource")
	if err != nil {
		return err
	}
	registry, err := effect.NewRegistry(e.Spec.Effects)
	if err != nil {
		return err
	}
	// No provider types are registered: objects are stored and read unstructured.
	a.api = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	a.dynamicStore = &DynamicStore{Client: cr.Observer{Registry: registry}.Dynamic(a.api, mapper), Context: a.ctx, Key: infraKey, InfraResource: infra.GVR(), ClaimResource: claim.GVR(), InfraKind: infra.GVK(), ClaimKind: claim.GVK()}
	return nil
}
func (a *Adapter) discoverDynamic() error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(a.dynamicStore.InfraKind)
	obj.SetName(infraKey.Name)
	obj.SetNamespace(infraKey.Namespace)
	obj.SetGeneration(1)
	if a.contractSatisfied {
		obj.SetAnnotations(map[string]string{"architecture.local/assignment-contract": "v1"})
	}
	_, err := a.api.Resource(a.dynamicStore.InfraResource).Namespace(infraKey.Namespace).Create(a.ctx, obj, metav1.CreateOptions{})
	return err
}
func (a *Adapter) fulfillDynamic() error {
	resource := a.api.Resource(a.dynamicStore.ClaimResource).Namespace(infraKey.Namespace)
	obj, err := resource.Get(a.ctx, "claim-for-"+infraKey.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if err = unstructured.SetNestedField(obj.Object, "machine-1", "status", "machineId"); err != nil {
		return err
	}
	_, err = resource.UpdateStatus(a.ctx, obj, metav1.UpdateOptions{})
	return err
}
func (a *Adapter) removeDynamic() error {
	return a.api.Resource(a.dynamicStore.InfraResource).Namespace(infraKey.Namespace).Delete(a.ctx, infraKey.Name, metav1.DeleteOptions{})
}

// Project the real fixture contents independently of the model and cached Store
// snapshots. Environment calls use the underlying fake API; MAC uses the observer.
func (a *Adapter) projectDynamic() (map[string]any, error) {
	state := map[string]any{"infraExists": false, "contractSatisfied": false, "claimExists": false, "statusMachineId": "", "claimMachineId": ""}
	infra, err := a.api.Resource(a.dynamicStore.InfraResource).Namespace(infraKey.Namespace).Get(a.ctx, infraKey.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		state["infraExists"] = true
		state["contractSatisfied"] = infra.GetAnnotations()["architecture.local/assignment-contract"] == "v1"
		id, _, err := unstructured.NestedString(infra.Object, "status", "machineId")
		if err != nil {
			return nil, err
		}
		state["statusMachineId"] = id
	}
	claim, err := a.api.Resource(a.dynamicStore.ClaimResource).Namespace(infraKey.Namespace).Get(a.ctx, "claim-for-"+infraKey.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		state["claimExists"] = true
		id, _, err := unstructured.NestedString(claim.Object, "status", "machineId")
		if err != nil {
			return nil, err
		}
		state["claimMachineId"] = id
		reference, _, err := unstructured.NestedString(claim.Object, "spec", "infraMachineName")
		if err != nil {
			return nil, err
		}
		if reference != infraKey.Name {
			return nil, fmt.Errorf("claim points at the wrong InfraMachine")
		}
	}
	return state, nil
}
