package ir

import (
	"fmt"
	"sort"
)

// Kinds of effect. An api effect goes through the Kubernetes API; an
// external effect calls out of it (a provisioning call, a cloud SDK, a
// webhook) and is identified by the symbol it calls rather than a resource.
const (
	KindAPI      = "api"
	KindExternal = "external"
)

// Operations an effect may perform against the API.
const (
	OpGet         = "get"
	OpList        = "list"
	OpCreate      = "create"
	OpUpdate      = "update"
	OpPatch       = "patch"
	OpDelete      = "delete"
	OpDeleteAllOf = "deleteAllOf"
	OpApply       = "apply"
)

// Consistency says what an effect reads or writes through: the informer
// cache, the API server directly, or only the schema (no runtime access).
const (
	ConsistencyCached = "cached"
	ConsistencyDirect = "direct"
	ConsistencySchema = "schema"
)

var (
	operations    = map[string]bool{OpGet: true, OpList: true, OpCreate: true, OpUpdate: true, OpPatch: true, OpDelete: true, OpDeleteAllOf: true, OpApply: true}
	consistencies = map[string]bool{ConsistencyCached: true, ConsistencyDirect: true, ConsistencySchema: true}
)

// Callee identifies the Go symbol an external effect calls: a package path,
// an optional receiver type and a function or method name.
type Callee struct {
	Package  string `yaml:"package"`
	Receiver string `yaml:"receiver,omitempty"`
	Func     string `yaml:"func"`
}

// Validate requires a package and a function name.
func (c Callee) Validate() error {
	if c.Package == "" || c.Func == "" {
		return fmt.Errorf("callee %q: package and func are required", c.String())
	}
	return nil
}

func (c Callee) String() string {
	if c.Receiver == "" {
		return c.Package + "." + c.Func
	}
	return c.Package + "." + c.Receiver + "." + c.Func
}

// Outcome is one declared result of an external effect. Its internals are
// never extracted: the guard says when the outcome applies, the updates say
// what it changes, and Error marks the outcomes the caller sees as failures.
// The order of outcomes on an effect is the declared order.
type Outcome struct {
	Name    string            `yaml:"name"`
	Guard   string            `yaml:"guard,omitempty"`
	Updates map[string]string `yaml:"updates,omitempty"`
	Error   bool              `yaml:"error,omitempty"`
}

// Effect is one operation an actor performs: an API operation on a resource
// (the default kind) or an external call identified by its callee.
type Effect struct {
	ID          string            `yaml:"id"`
	Kind        string            `yaml:"kind,omitempty"`
	Actor       string            `yaml:"actor"`
	Resource    GVK               `yaml:"resource,omitempty"`
	Operation   string            `yaml:"operation,omitempty"`
	Consistency string            `yaml:"consistency,omitempty"`
	Subresource string            `yaml:"subresource,omitempty"`
	Options     map[string]string `yaml:"options,omitempty"`
	Target      *Callee           `yaml:"target,omitempty"`
	Outcomes    []Outcome         `yaml:"outcomes,omitempty"`
	Evidence    Evidence          `yaml:"evidence"`
	Provenance  Provenance        `yaml:"provenance"`
}

// External reports whether the effect is of kind external.
func (e Effect) External() bool { return e.Kind == KindExternal }

// Validate checks the actor and evidence, then the fields its kind needs: an
// api effect has a resource, operation and consistency; an external effect
// has a target and at least one outcome, and since its internals are not
// extracted its evidence is declared or assumed.
func (e Effect) Validate() error {
	if e.Actor == "" {
		return fmt.Errorf("effect %s: actor is required", e.ID)
	}
	if err := e.Evidence.Validate(); err != nil {
		return fmt.Errorf("effect %s: %w", e.ID, err)
	}
	switch e.Kind {
	case "", KindAPI:
		return e.validateAPI()
	case KindExternal:
		return e.validateExternal()
	}
	return fmt.Errorf("effect %s: unknown kind %q; expected api or external", e.ID, e.Kind)
}

func (e Effect) validateAPI() error {
	if err := e.Resource.Validate(); err != nil {
		return fmt.Errorf("effect %s: %w", e.ID, err)
	}
	if !operations[e.Operation] {
		return fmt.Errorf("effect %s: unknown operation %q; expected get, list, create, update, patch, delete, deleteAllOf or apply", e.ID, e.Operation)
	}
	if !consistencies[e.Consistency] {
		return fmt.Errorf("effect %s: unknown consistency %q; expected cached, direct or schema", e.ID, e.Consistency)
	}
	if e.Target != nil || len(e.Outcomes) > 0 {
		return fmt.Errorf("effect %s: target and outcomes belong to external effects", e.ID)
	}
	return nil
}

func (e Effect) validateExternal() error {
	if e.Target == nil {
		return fmt.Errorf("effect %s: external effect requires a target", e.ID)
	}
	if err := e.Target.Validate(); err != nil {
		return fmt.Errorf("effect %s: %w", e.ID, err)
	}
	if len(e.Outcomes) == 0 {
		return fmt.Errorf("effect %s: external effect requires at least one outcome", e.ID)
	}
	names := map[string]bool{}
	for i, o := range e.Outcomes {
		if o.Name == "" {
			return fmt.Errorf("effect %s: outcome at index %d has no name", e.ID, i)
		}
		if names[o.Name] {
			return fmt.Errorf("effect %s: duplicate outcome %q", e.ID, o.Name)
		}
		names[o.Name] = true
	}
	if e.Evidence != Declared && e.Evidence != Assumed {
		return fmt.Errorf("effect %s: external effect internals are not extracted; evidence must be declared or assumed, got %s", e.ID, e.Evidence)
	}
	if e.Operation != "" || e.Consistency != "" || e.Subresource != "" || len(e.Options) > 0 {
		return fmt.Errorf("effect %s: operation, consistency, subresource and options belong to api effects", e.ID)
	}
	if e.Resource != (GVK{}) {
		if err := e.Resource.Validate(); err != nil {
			return fmt.Errorf("effect %s: %w", e.ID, err)
		}
	}
	return nil
}

// StableID derives an identifier independent of provenance and slice
// position: for an api effect from the actor, resource, operation,
// subresource and options; for an external effect from the actor and the
// target callee.
func (e Effect) StableID() string {
	if e.External() {
		var target string
		if e.Target != nil {
			target = e.Target.String()
		}
		return stableID("effect", e.Actor, KindExternal, target)
	}
	keys := make([]string, 0, len(e.Options))
	for k := range e.Options {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{"effect", e.Actor, e.Resource.String(), e.Operation, e.Subresource}
	for _, k := range keys {
		parts = append(parts, k+"="+e.Options[k])
	}
	return stableID(parts...)
}
