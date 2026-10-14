// Package provisioning demonstrates the Database controller's complete provisioning reconcile flow.
// Its Store is scoped to one Database; Kubernetes details stay in the adapter.
package database

// Store operations are integration boundaries, not controller decision logic.
// SatisfiesProvisioningContract represents discovery of the provider's
// supported contract. It is distinct from whether a instance has been provisioned.
type Store interface {
	DatabaseExists() bool
	SatisfiesProvisioningContract() bool
	DatabaseInstance() (exists bool, endpoint string)
	CreateDatabaseInstance()
	UpdateDatabaseStatus(endpoint string)
}

// Reconcile ensures a instance for a compatible Database, then publishes the
// provisioned instance's endpoint. Waiting returns control to the controller;
// a later instance event triggers another reconcile, rather than blocking here.
func Reconcile(store Store) string {
	found := store.DatabaseExists()
	if !found {
		return "absent"
	}
	compatible := store.SatisfiesProvisioningContract()
	if !compatible {
		return "unsupported"
	}
	instanceFound, endpoint := store.DatabaseInstance()
	if !instanceFound {
		store.CreateDatabaseInstance()
		return "waiting"
	}
	if endpoint == "" {
		return "waiting"
	}
	store.UpdateDatabaseStatus(endpoint)
	return "ready"
}
