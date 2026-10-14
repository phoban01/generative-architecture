// Package typed holds small, type-checkable controllers for goflow's typed
// extraction tests. It is loaded through go/packages, never built directly.
package typed

const maxRetries = 3

type Phase string

const (
	Ready   Phase = "ready"
	Waiting Phase = "waiting"
)

type Controller struct{}

// Score switches on typed constants, which lower to their literals.
func (c Controller) Score(phase Phase) int {
	switch phase {
	case Ready:
		return 2
	case Waiting:
		return 1
	}
	return 0
}

// Retry reports whether another attempt is allowed; the bound is a
// package-level constant that typed extraction lowers to its literal.
func (c Controller) Retry(attempts int) bool {
	if attempts >= maxRetries {
		return false
	}
	return true
}
