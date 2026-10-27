// Package v1beta1 is a fixture API package that names its group only with
// a marker, as controller-gen reads it.
// +groupName=other.example.test
package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Widget is a fixture kind.
type Widget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

// DeepCopyObject copies the Widget.
func (w *Widget) DeepCopyObject() runtime.Object {
	out := *w
	w.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	return &out
}
