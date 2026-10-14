// Package provisioner is an operation outside the Kubernetes API. Its
// internals are never extracted; the effects document in the parent package
// declares the outcomes of calling it.
package provisioner

// Provision requests an instance for id.
func Provision(id string) error { return nil }
