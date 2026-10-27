// Package protocol defines ProtocolContract, the behavioural semantics of a
// binding between a provider port and a consumer port. Port compatibility
// establishes connectivity only; a ProtocolContract says which writes to the
// bound state each role may make, in which order, and which properties must
// hold. Conformance to it is checked separately from binding.
package protocol

import (
	"fmt"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"regexp"
)

// Kind is the KRM kind of a protocol contract document.
const Kind = "ProtocolContract"

// Actors of a transition: the side of the binding that makes the write.
const (
	Provider = "provider"
	Consumer = "consumer"
)

// Violation is the protocol state a monitor enters when a role writes the
// bound state and no transition permits the write. A contract cannot declare
// it.
const Violation = "violation"

// Roles names the two sides of a binding. Provider is the side of the out
// port, Consumer the side of the in port. The names are for readers and
// reports; transitions refer to the sides as provider and consumer.
type Roles struct {
	Provider string `yaml:"provider"`
	Consumer string `yaml:"consumer"`
}

// Transition permits one write by Actor that moves the protocol from state
// From to state To. Guard is a CEL expression over port (the bound state
// before the write), next (the bound state after it) and state (the protocol
// state). An empty guard is true.
type Transition struct {
	Name  string          `yaml:"name"`
	From  string          `yaml:"from"`
	To    string          `yaml:"to"`
	Actor string          `yaml:"actor"`
	Guard expr.Expression `yaml:"guard,omitempty"`
}

// Property is an independently authored CEL invariant over state (the
// protocol state) and port (the bound state).
type Property struct {
	Name       string          `yaml:"name"`
	Expression expr.Expression `yaml:"expression"`
}

// Contract is the spec of a ProtocolContract document.
type Contract struct {
	Name        string       `yaml:"name"`
	Version     string       `yaml:"version"`
	Roles       Roles        `yaml:"roles"`
	States      []string     `yaml:"states"`
	Initial     string       `yaml:"initial"`
	Transitions []Transition `yaml:"transitions"`
	Properties  []Property   `yaml:"properties,omitempty"`
}

var identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// Load reads a ProtocolContract document strictly and validates it.
func Load(path string) (Contract, error) {
	var c Contract
	if _, err := krm.Read(path, Kind, &c); err != nil {
		return c, err
	}
	if err := c.Validate(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Validate checks the contract. Names and states are identifiers and unique;
// the initial state and every transition endpoint are declared states; every
// actor is provider or consumer; guards and properties parse as CEL. Errors
// name the spec field at fault.
func (c Contract) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("spec.name: required")
	}
	if c.Version == "" {
		return fmt.Errorf("spec.version: required")
	}
	if c.Roles.Provider == "" || c.Roles.Consumer == "" {
		return fmt.Errorf("spec.roles: provider and consumer names are required")
	}
	if c.Roles.Provider == c.Roles.Consumer {
		return fmt.Errorf("spec.roles: provider and consumer have the same name %q", c.Roles.Provider)
	}
	if len(c.States) == 0 {
		return fmt.Errorf("spec.states: at least one state is required")
	}
	states := map[string]bool{}
	for i, s := range c.States {
		if !identifier.MatchString(s) || s == Violation {
			return fmt.Errorf("spec.states[%d]: %q is not an identifier or is the reserved state %q", i, s, Violation)
		}
		if states[s] {
			return fmt.Errorf("spec.states[%d]: duplicate state %q", i, s)
		}
		states[s] = true
	}
	if !states[c.Initial] {
		return fmt.Errorf("spec.initial: unknown state %q", c.Initial)
	}
	if len(c.Transitions) == 0 {
		return fmt.Errorf("spec.transitions: at least one transition is required")
	}
	names := map[string]bool{}
	for i, t := range c.Transitions {
		at := fmt.Sprintf("spec.transitions[%d]", i)
		if !identifier.MatchString(t.Name) || names[t.Name] {
			return fmt.Errorf("%s.name: %q is not an identifier or is a duplicate", at, t.Name)
		}
		names[t.Name] = true
		if !states[t.From] {
			return fmt.Errorf("%s.from: unknown state %q", at, t.From)
		}
		if !states[t.To] {
			return fmt.Errorf("%s.to: unknown state %q", at, t.To)
		}
		if t.Actor != Provider && t.Actor != Consumer {
			return fmt.Errorf("%s.actor: unknown role %q; expected %s or %s", at, t.Actor, Provider, Consumer)
		}
		if t.Guard != "" {
			if _, err := expr.Rewrite(t.Guard, nil); err != nil {
				return fmt.Errorf("%s.guard: %w", at, err)
			}
		}
	}
	names = map[string]bool{}
	for i, p := range c.Properties {
		at := fmt.Sprintf("spec.properties[%d]", i)
		if !identifier.MatchString(p.Name) || names[p.Name] {
			return fmt.Errorf("%s.name: %q is not an identifier or is a duplicate", at, p.Name)
		}
		names[p.Name] = true
		if p.Expression == "" {
			return fmt.Errorf("%s.expression: required", at)
		}
		if _, err := expr.Rewrite(p.Expression, nil); err != nil {
			return fmt.Errorf("%s.expression: %w", at, err)
		}
	}
	return nil
}
