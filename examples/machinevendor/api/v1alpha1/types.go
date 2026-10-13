// Package v1alpha1 defines the API shapes used by the controller demo.
// +groupName=demo.architecture.local
// +kubebuilder:object:generate=true
package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// InfraMachine is an illustrative provider API, not a real CAPI InfraMachine.
// +kubebuilder:object:root=true
// +arch:resource
type InfraMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              InfraMachineSpec   `json:"spec"`
	Status            InfraMachineStatus `json:"status"`
}
type InfraMachineSpec struct {
	AllocationRequested bool `json:"allocationRequested"`
}

// +arch:implements=mac.allocation/v1
type InfraMachineStatus struct {
	MachineID string `json:"machineId"`
}

// MachineClaim carries the assignment obtained by the claim/lease mechanism.
// +kubebuilder:object:root=true
// +arch:resource
type MachineClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MachineClaimSpec `json:"spec"`
	Status            Assignment       `json:"status"`
}
type MachineClaimSpec struct {
	InfraMachineName string `json:"infraMachineName"`
}
type Assignment struct {
	MachineID string `json:"machineId"`
	Epoch     int64  `json:"epoch"`
}
