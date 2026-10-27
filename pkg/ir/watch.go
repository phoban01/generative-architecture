package ir

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Controller is one reconciler discovered in a project. Injection lists the
// exported fields through which it receives its dependencies; see
// docs/supported-patterns.md for the field types a frontend records.
type Controller struct {
	ID         string     `yaml:"id"`
	Name       string     `yaml:"name"`
	Package    string     `yaml:"package,omitempty"`
	Receiver   string     `yaml:"receiver,omitempty"`
	Setup      *Source    `yaml:"setup,omitempty"`
	Injection  []Field    `yaml:"injection,omitempty"`
	Provenance Provenance `yaml:"provenance"`
}

// Field is an exported struct field by name and fully qualified Go type.
type Field struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

// Predicate filters the events a watch delivers.
type Predicate struct {
	Kind string            `yaml:"kind"`
	Args map[string]string `yaml:"args,omitempty"`
}

// Mapping kinds: how a watched object is turned into reconcile keys.
const (
	MappingSelf   = "self"   // the object's own key
	MappingField  = "field"  // a field path on the object names the key
	MappingOwner  = "owner"  // the owner reference of the given GVK
	MappingCustom = "custom" // a mapper the extractor could not summarize
)

// KeyMapping says how events on a watched resource become reconcile keys.
type KeyMapping struct {
	Kind      string   `yaml:"kind"`
	FieldPath []string `yaml:"fieldPath,omitempty"`
	OwnerGVK  *GVK     `yaml:"ownerGVK,omitempty"`
	// OnlyController limits an owner mapping to the controlling owner.
	OnlyController bool `yaml:"onlyController,omitempty"`
}

// Validate checks the mapping kind and its required arguments.
func (m KeyMapping) Validate() error {
	switch m.Kind {
	case MappingSelf, MappingCustom:
	case MappingField:
		if len(m.FieldPath) == 0 {
			return fmt.Errorf("field mapping requires a field path")
		}
	case MappingOwner:
		if m.OwnerGVK == nil {
			return fmt.Errorf("owner mapping requires an owner GVK")
		}
		if err := m.OwnerGVK.Validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown mapping kind %q; expected self, field, owner or custom", m.Kind)
	}
	return nil
}

// Watch is one event source registered by a controller.
type Watch struct {
	ID         string      `yaml:"id"`
	Controller string      `yaml:"controller"`
	Resource   GVK         `yaml:"resource"`
	Primary    bool        `yaml:"primary,omitempty"`
	Predicates []Predicate `yaml:"predicates,omitempty"`
	Mapping    KeyMapping  `yaml:"mapping"`
	Evidence   Evidence    `yaml:"evidence"`
	Provenance Provenance  `yaml:"provenance"`
}

// StableID derives an identifier from the controller, resource and mapping,
// independent of predicate order, source position and slice position.
func (w Watch) StableID() string {
	return stableID("watch", w.Controller, w.Resource.String(), w.Mapping.Kind, strings.Join(w.Mapping.FieldPath, "."))
}

// Validate checks the resource, mapping and evidence of the watch.
func (w Watch) Validate() error {
	if w.Controller == "" {
		return fmt.Errorf("watch %s: controller is required", w.ID)
	}
	if err := w.Resource.Validate(); err != nil {
		return fmt.Errorf("watch %s: %w", w.ID, err)
	}
	if err := w.Mapping.Validate(); err != nil {
		return fmt.Errorf("watch %s: %w", w.ID, err)
	}
	if err := w.Evidence.Validate(); err != nil {
		return fmt.Errorf("watch %s: %w", w.ID, err)
	}
	return nil
}

// stableID hashes its parts, separated so that no two part lists collide by
// concatenation, and returns the first 16 hex characters.
func stableID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:%s\x00", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
