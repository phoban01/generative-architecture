// Package v1alpha1 defines the API types of the neutral reference system: a
// Tenant that owns a namespace, a Database provisioned for a tenant, and a
// Backup taken of a database. The three controllers interact only through
// these resources (see docs/adr/0004-reference-system.md).
// +groupName=refsystem.example.org
// +kubebuilder:object:generate=true
package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Tenant requests an isolated namespace with a resource quota.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +arch:resource
type Tenant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TenantSpec   `json:"spec"`
	Status            TenantStatus `json:"status,omitempty"`
}

// TenantSpec is the desired state of a Tenant.
type TenantSpec struct {
	// Quota names the quota profile applied to the tenant's namespace.
	Quota string `json:"quota"`
}

// TenantStatus is the observed state of a Tenant.
type TenantStatus struct {
	// NamespaceReady is true once the tenant's namespace and quota exist.
	NamespaceReady bool `json:"namespaceReady"`
}

// TenantList contains a list of Tenant.
// +kubebuilder:object:root=true
type TenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tenant `json:"items"`
}

// Database requests a database for a tenant.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +arch:resource
type Database struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DatabaseSpec   `json:"spec"`
	Status            DatabaseStatus `json:"status,omitempty"`
}

// DatabaseSpec is the desired state of a Database.
type DatabaseSpec struct {
	// Tenant is the name of the owning Tenant.
	Tenant string `json:"tenant"`
	// Size names the capacity profile of the database.
	Size string `json:"size"`
}

// DatabaseStatus is the observed state of a Database.
type DatabaseStatus struct {
	// Ready is true once the database accepts connections.
	Ready bool `json:"ready"`
	// SecretName names the Secret holding the connection credentials.
	SecretName string `json:"secretName,omitempty"`
}

// DatabaseList contains a list of Database.
// +kubebuilder:object:root=true
type DatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Database `json:"items"`
}

// Backup requests a backup of a database.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +arch:resource
type Backup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackupSpec   `json:"spec"`
	Status            BackupStatus `json:"status,omitempty"`
}

// BackupSpec is the desired state of a Backup.
type BackupSpec struct {
	// Database is the name of the Database to back up.
	Database string `json:"database"`
}

// BackupStatus is the observed state of a Backup.
type BackupStatus struct {
	// Phase is one of Pending, Running, Completed or Failed.
	Phase string `json:"phase,omitempty"`
}

// BackupList contains a list of Backup.
// +kubebuilder:object:root=true
type BackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Backup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Tenant{}, &TenantList{}, &Database{}, &DatabaseList{}, &Backup{}, &BackupList{})
}
