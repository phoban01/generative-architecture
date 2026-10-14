package database

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

// DynamicStore is the Database controller's I/O implementation for provider APIs with no Go types.
// GVRs/GVKs are supplied by discovery or the bounded resource catalog. The small
// Store interface retains errors here so the replay adapter can reject them.
type DynamicStore struct {
	Client                             dynamic.Interface
	Context                            context.Context
	Key                                client.ObjectKey
	DatabaseResource, InstanceResource schema.GroupVersionResource
	DatabaseKind, InstanceKind         schema.GroupVersionKind
	database                           *unstructured.Unstructured
	Err                                error
}

func (s *DynamicStore) DatabaseExists() bool {
	obj, err := s.Client.Resource(s.DatabaseResource).Namespace(s.Key.Namespace).Get(s.Context, s.Key.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		s.database = nil
		return false
	}
	if err != nil {
		s.Err = err
		return false
	}
	s.database = obj
	return true
}
func (s *DynamicStore) SatisfiesProvisioningContract() bool {
	return s.database != nil && s.database.GetAnnotations()["architecture.local/provisioning-contract"] == "v1"
}
func (s *DynamicStore) DatabaseInstance() (bool, string) {
	obj, err := s.Client.Resource(s.InstanceResource).Namespace(s.Key.Namespace).Get(s.Context, "instance-for-"+s.Key.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, ""
	}
	if err != nil {
		s.Err = err
		return false, ""
	}
	id, _, err := unstructured.NestedString(obj.Object, "status", "endpoint")
	if err != nil {
		s.Err = err
		return false, ""
	}
	return true, id
}
func (s *DynamicStore) CreateDatabaseInstance() {
	obj := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"databaseName": s.Key.Name}}}
	obj.SetGroupVersionKind(s.InstanceKind)
	obj.SetName("instance-for-" + s.Key.Name)
	obj.SetNamespace(s.Key.Namespace)
	_, s.Err = s.Client.Resource(s.InstanceResource).Namespace(s.Key.Namespace).Create(s.Context, obj, metav1.CreateOptions{})
}
func (s *DynamicStore) UpdateDatabaseStatus(endpoint string) {
	if s.database == nil {
		s.Err = fmt.Errorf("Database was not read")
		return
	}
	obj := s.database.DeepCopy()
	if err := unstructured.SetNestedField(obj.Object, endpoint, "status", "endpoint"); err != nil {
		s.Err = err
		return
	}
	_, s.Err = s.Client.Resource(s.DatabaseResource).Namespace(s.Key.Namespace).UpdateStatus(s.Context, obj, metav1.UpdateOptions{})
}
