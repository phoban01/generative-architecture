package ir

import (
	"fmt"

	"generative-architecture/pkg/krm"
)

// SchemaVersion is the IR document version this package reads and writes.
const SchemaVersion = "v1alpha1"

// Kind is the KRM kind of an IR document.
const Kind = "ArchitectureIR"

// Constraint is an independently authored property over model state.
type Constraint struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	Expression string `yaml:"expression"`
	Scope      string `yaml:"scope,omitempty"`
}

// ContractImplementation records that a Go type implements a versioned
// contract, with the JSON path each contract field maps to.
type ContractImplementation struct {
	Contract   string            `yaml:"contract"`
	Version    string            `yaml:"version"`
	Type       string            `yaml:"type"`
	JSONPaths  map[string]string `yaml:"jsonPaths,omitempty"`
	Evidence   Evidence          `yaml:"evidence"`
	Provenance Provenance        `yaml:"provenance"`
}

// ID identifies the implementation by contract, version and type.
func (c ContractImplementation) ID() string {
	return c.Contract + "/" + c.Version + "@" + c.Type
}

// Document is the top-level IR: everything the frontends learned about a
// project, consumed by every generator.
type Document struct {
	Schema      string                   `yaml:"schema"`
	Controllers []Controller             `yaml:"controllers,omitempty"`
	Watches     []Watch                  `yaml:"watches,omitempty"`
	Effects     []Effect                 `yaml:"effects,omitempty"`
	Processes   []Process                `yaml:"processes,omitempty"`
	Contracts   []ContractImplementation `yaml:"contracts,omitempty"`
	Constraints []Constraint             `yaml:"constraints,omitempty"`
}

// Validate checks the schema version and that IDs are unique within each
// list and that every watch and effect is itself valid.
func (d Document) Validate() error {
	if d.Schema != SchemaVersion {
		return fmt.Errorf("unsupported IR schema %q; expected %s", d.Schema, SchemaVersion)
	}
	if err := unique("controller", len(d.Controllers), func(i int) string { return d.Controllers[i].ID }); err != nil {
		return err
	}
	if err := unique("watch", len(d.Watches), func(i int) string { return d.Watches[i].ID }); err != nil {
		return err
	}
	if err := unique("effect", len(d.Effects), func(i int) string { return d.Effects[i].ID }); err != nil {
		return err
	}
	if err := unique("process", len(d.Processes), func(i int) string { return d.Processes[i].ID }); err != nil {
		return err
	}
	if err := unique("contract implementation", len(d.Contracts), func(i int) string { return d.Contracts[i].ID() }); err != nil {
		return err
	}
	if err := unique("constraint", len(d.Constraints), func(i int) string { return d.Constraints[i].ID }); err != nil {
		return err
	}
	for _, w := range d.Watches {
		if err := w.Validate(); err != nil {
			return err
		}
	}
	for _, e := range d.Effects {
		if err := e.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func unique(what string, n int, id func(int) string) error {
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		v := id(i)
		if v == "" {
			return fmt.Errorf("%s at index %d has no id", what, i)
		}
		if seen[v] {
			return fmt.Errorf("duplicate %s id %q", what, v)
		}
		seen[v] = true
	}
	return nil
}

// Write validates the document and writes it as an ArchitectureIR status
// document named name.
func (d Document) Write(path, name string) error {
	if err := d.Validate(); err != nil {
		return err
	}
	return krm.WriteResult(path, Kind, name, d)
}

// Read loads and validates an ArchitectureIR document.
func Read(path string) (Document, error) {
	var d Document
	if err := krm.ReadResult(path, Kind, &d); err != nil {
		return d, err
	}
	return d, d.Validate()
}
