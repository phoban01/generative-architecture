// Package assignment demonstrates MAC's complete assignment reconcile flow.
// Its Store is scoped to one InfraMachine; Kubernetes details stay in the adapter.
package assignment

// Store operations are integration boundaries, not controller decision logic.
// SatisfiesMachineAssignmentContract represents discovery of the provider's
// supported contract. It is distinct from whether a claim has been fulfilled.
type Store interface {
	InfraMachineExists() bool
	SatisfiesMachineAssignmentContract() bool
	MachineClaim() (exists bool, machineID string)
	CreateMachineClaim()
	UpdateInfraMachineStatus(machineID string)
}

// Reconcile ensures a claim for a compatible InfraMachine, then publishes the
// fulfilled claim's machine ID. Waiting returns control to the controller;
// a later claim event triggers another reconcile, rather than blocking here.
func Reconcile(store Store) string {
	found := store.InfraMachineExists()
	if !found {
		return "absent"
	}
	compatible := store.SatisfiesMachineAssignmentContract()
	if !compatible {
		return "unsupported"
	}
	claimFound, machineID := store.MachineClaim()
	if !claimFound {
		store.CreateMachineClaim()
		return "waiting"
	}
	if machineID == "" {
		return "waiting"
	}
	store.UpdateInfraMachineStatus(machineID)
	return "assigned"
}
