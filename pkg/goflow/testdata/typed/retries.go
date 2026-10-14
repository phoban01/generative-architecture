// Package typed holds small, type-checkable controllers for goflow's typed
// extraction tests. It is loaded through go/packages, never built directly.
package typed

const maxRetries = 3

type Controller struct{}

// Retry reports whether another attempt is allowed; the bound is a
// package-level constant that typed extraction lowers to its literal.
func (c Controller) Retry(attempts int) bool {
	if attempts >= maxRetries {
		return false
	}
	return true
}
