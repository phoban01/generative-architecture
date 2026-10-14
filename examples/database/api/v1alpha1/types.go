// Package v1alpha1 defines the API types of the sample database controller.
// +groupName=demo.architecture.local
// +kubebuilder:object:generate=true
package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Database asks for a provisioned database. The sample controller publishes the
// endpoint of the provisioned instance into its status.
// +kubebuilder:object:root=true
// +arch:resource
type Database struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DatabaseSpec   `json:"spec"`
	Status            DatabaseStatus `json:"status"`
}
type DatabaseSpec struct {
	Engine string `json:"engine"`
}
type DatabaseStatus struct {
	Endpoint string `json:"endpoint"`
}

// DatabaseInstance is created by the sample controller for one Database and
// provisioned by a separate subsystem that fills in the endpoint.
// +kubebuilder:object:root=true
// +arch:resource
type DatabaseInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DatabaseInstanceSpec   `json:"spec"`
	Status            DatabaseInstanceStatus `json:"status"`
}
type DatabaseInstanceSpec struct {
	DatabaseName string `json:"databaseName"`
}
type DatabaseInstanceStatus struct {
	Endpoint string `json:"endpoint"`
}
