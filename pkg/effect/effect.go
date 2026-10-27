// Package effect describes observable boundaries independently of any runtime.
package effect

import (
	"context"
	"fmt"
)

// Resource is a versioned resource identity, not a Go type name.
type Resource struct {
	Group   string `yaml:"group"`
	Version string `yaml:"version"`
	Kind    string `yaml:"kind"`
}
type Descriptor struct {
	Actor       string   `yaml:"actor,omitempty"`
	ID          string   `yaml:"id"`
	Resource    Resource `yaml:"resource"`
	Operation   string   `yaml:"operation"`
	Consistency string   `yaml:"consistency"`
}

// Registry is immutable after construction. Ambiguous bindings are rejected.
type Registry struct{ entries []Descriptor }

func NewRegistry(entries []Descriptor) (*Registry, error) {
	ids := map[string]bool{}
	signatures := map[string]bool{}
	for _, e := range entries {
		if e.ID == "" || e.Resource.Version == "" || e.Resource.Kind == "" || e.Operation == "" || e.Consistency == "" {
			return nil, fmt.Errorf("incomplete effect: %+v", e)
		}
		signature := fmt.Sprintf("%q/%q/%q/%q/%q/%q", e.Actor, e.Resource.Group, e.Resource.Version, e.Resource.Kind, e.Operation, e.Consistency)
		if ids[e.ID] || signatures[signature] {
			return nil, fmt.Errorf("duplicate or ambiguous effect %q", e.ID)
		}
		ids[e.ID] = true
		signatures[signature] = true
	}
	return &Registry{entries: append([]Descriptor(nil), entries...)}, nil
}
func (r *Registry) Lookup(resource Resource, operation, consistency string) (Descriptor, error) {
	return r.LookupFor("", resource, operation, consistency)
}
func (r *Registry) LookupFor(actor string, resource Resource, operation, consistency string) (Descriptor, error) {
	if r != nil {
		for _, e := range r.entries {
			if e.Actor == actor && e.Resource == resource && e.Operation == operation && e.Consistency == consistency {
				return e, nil
			}
		}
	}
	return Descriptor{}, fmt.Errorf("unsummarized effect: %v %s (%s)", resource, operation, consistency)
}
func (r *Registry) Descriptors() []Descriptor { return append([]Descriptor(nil), r.entries...) }

type Key struct{ Namespace, Name string }
type Call struct {
	Invocation string
	Descriptor Descriptor
	Key        Key
	// Snapshots are detached from implementation objects. Hooks may inspect them.
	Submitted map[string]any
	// Options are the call's recorded options, such as patchType, patch,
	// labelSelector or fieldManager. Nil when the call has none.
	Options map[string]string
}
type Result struct {
	Snapshot map[string]any
	Err      error
}
type Hook func(context.Context, Call, func() Result) Result

// Gate pauses before each effect. Use one Gate per invocation. Cancel the
// invocation context on every exit path to release a partially replayed process.
type Gate struct{ calls chan *Boundary }
type Boundary struct {
	Call    Call
	release chan error
	done    chan Result
}

func NewGate() *Gate { return &Gate{calls: make(chan *Boundary)} }
func (g *Gate) Hook(ctx context.Context, call Call, execute func() Result) Result {
	b := &Boundary{Call: call, release: make(chan error), done: make(chan Result, 1)}
	select {
	case g.calls <- b:
	case <-ctx.Done():
		return Result{Err: ctx.Err()}
	}
	var injected error
	select {
	case injected = <-b.release:
	case <-ctx.Done():
		return Result{Err: ctx.Err()}
	}
	result := Result{Err: injected}
	if injected == nil {
		result = execute()
	}
	b.done <- result
	return result
}
func (g *Gate) Next(ctx context.Context) (*Boundary, error) {
	select {
	case b := <-g.calls:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Complete executes the real operation, or injects the supplied error without
// executing it. Call at most once for a boundary.
func (b *Boundary) Complete(ctx context.Context, injected error) (Result, error) {
	select {
	case b.release <- injected:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	select {
	case r := <-b.done:
		return r, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}
