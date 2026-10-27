package conformance

import (
	"errors"
	"fmt"
	"github.com/phoban01/generative-architecture/pkg/goflow"
)

// Category classifies why a replay or check failed so that reports can
// distinguish a model violation from a disagreeing implementation, an
// unsupported construct or a broken tool.
type Category string

const (
	// CategorySafety: a model property was violated on a trace state.
	CategorySafety Category = "safety"
	// CategoryConformance: the trace, the original CEL or the implementation
	// projection disagree with one another.
	CategoryConformance Category = "conformance"
	// CategoryUnsupported: an adapter reported a construct the frontends
	// cannot handle (a goflow.Diagnostic).
	CategoryUnsupported Category = "unsupported"
	// CategoryTool: everything else, such as invalid inputs, cancellation and
	// failures of the adapter or external tools.
	CategoryTool Category = "tool"
)

// Failure is a categorized replay error. Step is the index of the trace state
// being checked and Event its action ("init" for the initial state); both are
// zero-valued when the failure precedes the first state.
type Failure struct {
	Category Category
	Step     int
	Event    string
	Err      error
}

func (f *Failure) Error() string {
	if f.Err == nil {
		return string(f.Category) + " failure"
	}
	return fmt.Sprintf("%s: %v", f.Category, f.Err)
}
func (f *Failure) Unwrap() error { return f.Err }

// AsFailure reports whether err is, or wraps, a *Failure.
func AsFailure(err error) (*Failure, bool) {
	var f *Failure
	if errors.As(err, &f) {
		return f, true
	}
	return nil, false
}

// CategoryOf classifies any error: a wrapped Failure keeps its category, a
// goflow.Diagnostic is unsupported and everything else is a tool failure.
// A nil error has no category.
func CategoryOf(err error) Category {
	if err == nil {
		return ""
	}
	if f, ok := AsFailure(err); ok {
		return f.Category
	}
	if _, ok := goflow.AsDiagnostic(err); ok {
		return CategoryUnsupported
	}
	return CategoryTool
}

// fail wraps err with the category implied by its type unless an explicit
// category is given: adapter diagnostics are unsupported, other errors tool.
func fail(category Category, step int, event string, err error) error {
	if category == "" {
		category = CategoryOf(err)
	}
	return &Failure{Category: category, Step: step, Event: event, Err: err}
}
