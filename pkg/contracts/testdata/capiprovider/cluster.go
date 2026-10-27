package capiprovider

// ClusterStatusError mirrors the named string type that Cluster API uses for
// status.failureReason of a cluster.
type ClusterStatusError string

// APIEndpoint mirrors the Cluster API endpoint type.
type APIEndpoint struct {
	Host string `json:"host"`
	Port int32  `json:"port"`
}

// InfraCluster follows the InfrastructureCluster contract v1beta1.
type InfraCluster struct {
	Spec   InfraClusterSpec   `json:"spec,omitempty"`
	Status InfraClusterStatus `json:"status,omitempty"`
}

type InfraClusterSpec struct {
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint"`
}

type InfraClusterStatus struct {
	Ready          bool                `json:"ready"`
	FailureReason  *ClusterStatusError `json:"failureReason,omitempty"`
	FailureMessage *string             `json:"failureMessage,omitempty"`
}

// ExternalEndpointCluster follows the contract without a control plane
// endpoint, because another component provides it.
type ExternalEndpointCluster struct {
	Status struct {
		Ready bool `json:"ready"`
	} `json:"status"`
}

// ClusterWithIntPort breaks the contract: the port is a Go int, not int32.
type ClusterWithIntPort struct {
	Spec struct {
		ControlPlaneEndpoint struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		} `json:"controlPlaneEndpoint"`
	} `json:"spec"`
	Status InfraClusterStatus `json:"status"`
}

// ClusterWithoutReady breaks the contract: status.ready is missing.
type ClusterWithoutReady struct {
	Spec   InfraClusterSpec `json:"spec"`
	Status struct{}         `json:"status"`
}

// BootstrapConfig follows the BootstrapConfig contract v1beta1.
type BootstrapConfig struct {
	Spec   BootstrapConfigSpec   `json:"spec,omitempty"`
	Status BootstrapConfigStatus `json:"status,omitempty"`
}

type BootstrapConfigSpec struct {
	Format string `json:"format,omitempty"`
}

type BootstrapConfigStatus struct {
	Ready          bool    `json:"ready"`
	DataSecretName *string `json:"dataSecretName,omitempty"`
	FailureReason  *string `json:"failureReason,omitempty"`
	FailureMessage *string `json:"failureMessage,omitempty"`
}

// BootstrapWithoutDataSecretName breaks the contract: the bootstrap data
// secret is missing.
type BootstrapWithoutDataSecretName struct {
	Status struct {
		Ready bool `json:"ready"`
	} `json:"status"`
}
