// Package widgets is a fixture API package whose name is not its version.
// Its GroupVersion variable names the group and version.
package widgets

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const group = "widgets.example.test"

// GroupVersion is the group and version of this package.
var GroupVersion = schema.GroupVersion{Group: group, Version: "v1beta1"}

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
