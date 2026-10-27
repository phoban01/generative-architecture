// Package external holds a reconciler that calls out of the Kubernetes API
// for goflow's external-effect tests. The call's outcomes are declared in
// effects.yaml rather than extracted. It is loaded through go/packages,
// never built directly.
package external

import "github.com/phoban01/generative-architecture/pkg/goflow/testdata/external/provisioner"

type Reconciler struct{}

// Reconcile requests provisioning and reports the result.
func (r *Reconciler) Reconcile(id string) (string, error) {
	if err := provisioner.Provision(id); err != nil {
		return "", err
	}
	return "provisioned", nil
}

// Helper calls a local function no document declares.
func (r *Reconciler) Helper(id string) (string, error) {
	normalize(id)
	return "ready", nil
}

func normalize(id string) string { return id }
