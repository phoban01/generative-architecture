// Package capiprovider mimics the API types of a Cluster API provider. The
// contract tests resolve it as a dependency module, the same way they would
// resolve a real provider from the module cache. It has no imports, so the
// tests need no network and no cluster-api module.
package capiprovider

// MachineStatusError mirrors the named string type that Cluster API uses for
// status.failureReason.
type MachineStatusError string

// InfraMachine follows the InfrastructureMachine contract v1beta1, with all
// optional fields.
type InfraMachine struct {
	Spec   InfraMachineSpec   `json:"spec,omitempty"`
	Status InfraMachineStatus `json:"status,omitempty"`
}

type InfraMachineSpec struct {
	ProviderID    *string `json:"providerID,omitempty"`
	FailureDomain *string `json:"failureDomain,omitempty"`
	ImageID       string  `json:"imageID"`
}

type InfraMachineStatus struct {
	Ready          bool                `json:"ready"`
	FailureReason  *MachineStatusError `json:"failureReason,omitempty"`
	FailureMessage *string             `json:"failureMessage,omitempty"`
}

// MinimalInfraMachine follows the v1beta1 contract with required fields only.
type MinimalInfraMachine struct {
	Spec struct {
		ProviderID string `json:"providerID"`
	} `json:"spec"`
	Status struct {
		Ready bool `json:"ready"`
	} `json:"status"`
}

// InfraMachineV1Beta2 follows the InfrastructureMachine contract v1beta2.
type InfraMachineV1Beta2 struct {
	Spec   InfraMachineSpecV1Beta2   `json:"spec,omitempty"`
	Status InfraMachineStatusV1Beta2 `json:"status,omitempty"`
}

type InfraMachineSpecV1Beta2 struct {
	ProviderID string `json:"providerID,omitempty"`
}

type InfraMachineStatusV1Beta2 struct {
	Initialization *MachineInitialization `json:"initialization,omitempty"`
	FailureDomain  string                 `json:"failureDomain,omitempty"`
}

type MachineInitialization struct {
	Provisioned *bool `json:"provisioned,omitempty"`
}

// MachineWithoutProviderID breaks the contract: spec.providerID is missing.
type MachineWithoutProviderID struct {
	Spec   struct{} `json:"spec"`
	Status struct {
		Ready bool `json:"ready"`
	} `json:"status"`
}

// MachineWithStringReady breaks the contract: status.ready is not a bool.
type MachineWithStringReady struct {
	Spec   InfraMachineSpec `json:"spec"`
	Status struct {
		Ready string `json:"ready"`
	} `json:"status"`
}

// MachineWithBoolFailureReason breaks the contract: the optional
// status.failureReason is present with the wrong type.
type MachineWithBoolFailureReason struct {
	Spec   InfraMachineSpec `json:"spec"`
	Status struct {
		Ready         bool `json:"ready"`
		FailureReason bool `json:"failureReason"`
	} `json:"status"`
}
