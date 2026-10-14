package ir

import (
	"fmt"
	"sort"
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

// Effect is one API operation an actor performs on a resource.
type Effect struct {
	ID          string            `yaml:"id"`
	Actor       string            `yaml:"actor"`
	Resource    GVK               `yaml:"resource"`
	Operation   string            `yaml:"operation"`
	Consistency string            `yaml:"consistency"`
	Subresource string            `yaml:"subresource,omitempty"`
	Options     map[string]string `yaml:"options,omitempty"`
	Evidence    Evidence          `yaml:"evidence"`
	Provenance  Provenance        `yaml:"provenance"`
}

// Validate checks the resource, operation, consistency and evidence.
func (e Effect) Validate() error {
	if e.Actor == "" {
		return fmt.Errorf("effect %s: actor is required", e.ID)
	}
	if err := e.Resource.Validate(); err != nil {
		return fmt.Errorf("effect %s: %w", e.ID, err)
	}
	if !operations[e.Operation] {
		return fmt.Errorf("effect %s: unknown operation %q; expected get, list, create, update, patch, delete, deleteAllOf or apply", e.ID, e.Operation)
	}
	if !consistencies[e.Consistency] {
		return fmt.Errorf("effect %s: unknown consistency %q; expected cached, direct or schema", e.ID, e.Consistency)
	}
	if err := e.Evidence.Validate(); err != nil {
		return fmt.Errorf("effect %s: %w", e.ID, err)
	}
	return nil
}

// StableID derives an identifier from the actor, resource, operation,
// subresource and options, independent of provenance and slice position.
func (e Effect) StableID() string {
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
