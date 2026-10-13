package assignment

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DynamicStore is MAC's I/O implementation for provider APIs with no Go types.
// GVRs/GVKs are supplied by discovery or the bounded resource catalog. The small
// Store interface retains errors here so the replay adapter can reject them.
type DynamicStore struct {
	Client                       dynamic.Interface
	Context                      context.Context
	Key                          client.ObjectKey
	InfraResource, ClaimResource schema.GroupVersionResource
	InfraKind, ClaimKind         schema.GroupVersionKind
	infra                        *unstructured.Unstructured
	Err                          error
}

func (s *DynamicStore) InfraMachineExists() bool {
	obj, err := s.Client.Resource(s.InfraResource).Namespace(s.Key.Namespace).Get(s.Context, s.Key.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		s.infra = nil
		return false
	}
	if err != nil {
		s.Err = err
		return false
	}
	s.infra = obj
	return true
}
func (s *DynamicStore) SatisfiesMachineAssignmentContract() bool {
	return s.infra != nil && s.infra.GetAnnotations()["architecture.local/assignment-contract"] == "v1"
}
func (s *DynamicStore) MachineClaim() (bool, string) {
	obj, err := s.Client.Resource(s.ClaimResource).Namespace(s.Key.Namespace).Get(s.Context, "claim-for-"+s.Key.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, ""
	}
	if err != nil {
		s.Err = err
		return false, ""
	}
	id, _, err := unstructured.NestedString(obj.Object, "status", "machineId")
	if err != nil {
		s.Err = err
		return false, ""
	}
	return true, id
}
func (s *DynamicStore) CreateMachineClaim() {
	obj := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"infraMachineName": s.Key.Name}}}
	obj.SetGroupVersionKind(s.ClaimKind)
	obj.SetName("claim-for-" + s.Key.Name)
	obj.SetNamespace(s.Key.Namespace)
	_, s.Err = s.Client.Resource(s.ClaimResource).Namespace(s.Key.Namespace).Create(s.Context, obj, metav1.CreateOptions{})
}
func (s *DynamicStore) UpdateInfraMachineStatus(machineID string) {
	if s.infra == nil {
		s.Err = fmt.Errorf("InfraMachine was not read")
		return
	}
	obj := s.infra.DeepCopy()
	if err := unstructured.SetNestedField(obj.Object, machineID, "status", "machineId"); err != nil {
		s.Err = err
		return
	}
	_, s.Err = s.Client.Resource(s.InfraResource).Namespace(s.Key.Namespace).UpdateStatus(s.Context, obj, metav1.UpdateOptions{})
}
