package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// Temporal is a liveness property over the states of a model. Exactly one
// of Eventually and LeadsTo is set. Both take CEL state predicates with
// the scope of a Property.
type Temporal struct {
	Name string `yaml:"name"`
	// Eventually holds when every fair behaviour reaches a state that
	// satisfies the predicate.
	Eventually expr.Expression `yaml:"eventually,omitempty"`
	// LeadsTo holds when, in every fair behaviour, each state that
	// satisfies From is followed, then or later, by a state that
	// satisfies To.
	LeadsTo *LeadsTo `yaml:"leadsTo,omitempty"`
}

// LeadsTo is the pair of predicates of a leads-to property.
type LeadsTo struct {
	From expr.Expression `yaml:"from"`
	To   expr.Expression `yaml:"to"`
}

// Fairness names the actions that a liveness check assumes are fair. A
// weakly fair action that stays enabled is eventually taken. A strongly
// fair action that is enabled infinitely often is eventually taken.
type Fairness struct {
	Weak   []string `yaml:"weak,omitempty"`
	Strong []string `yaml:"strong,omitempty"`
}

// Empty reports whether no action is assumed fair.
func (f Fairness) Empty() bool { return len(f.Weak) == 0 && len(f.Strong) == 0 }

// String lists the assumption, for example "weak(a, b) strong(c)".
func (f Fairness) String() string {
	var parts []string
	if len(f.Weak) > 0 {
		parts = append(parts, "weak("+strings.Join(f.Weak, ", ")+")")
	}
	if len(f.Strong) > 0 {
		parts = append(parts, "strong("+strings.Join(f.Strong, ", ")+")")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

// Constraints is the payload of a ModelConstraints document: per-state
// properties, temporal properties and the fairness that the temporal
// properties assume.
type Constraints struct {
	Properties []Property `yaml:"properties"`
	Temporal   []Temporal `yaml:"temporal,omitempty"`
	Fairness   Fairness   `yaml:"fairness,omitempty"`
}

// ErrNoFairness is returned for a temporal property without a fairness
// assumption. Without one a model may stutter forever, so a liveness
// check would prove nothing about progress.
var ErrNoFairness = errors.New("no fairness assumption declared; a liveness property needs at least one weakly or strongly fair action")

// TemporalModule returns the Quint module of m with t added as a temporal
// definition of the same name. The definition is "fairness implies
// formula", with fair as the fairness. It fails closed on an unknown or
// repeated fair action, a name that the model already uses and a predicate
// outside the CEL subset. When the definition is valid but fair is empty,
// the error wraps ErrNoFairness.
func (m *Compiled) TemporalModule(t Temporal, fair Fairness) (string, error) {
	fail := func(err error) (string, error) { return "", fmt.Errorf("temporal %s: %w", t.Name, err) }
	if !ident.MatchString(t.Name) || reserved[t.Name] || m.uses(t.Name) {
		return fail(fmt.Errorf("invalid or duplicate model identifier %q", t.Name))
	}
	actions := make([]string, len(m.spec.Actions))
	for i, a := range m.spec.Actions {
		actions[i] = a.Name
	}
	vars := append(names(m.spec.State), "event")
	tuple := "(" + strings.Join(vars, ", ") + ")"
	var assumptions []string
	seen := map[string]bool{}
	for _, list := range []struct {
		kind  string
		names []string
	}{{"weakFair", fair.Weak}, {"strongFair", fair.Strong}} {
		for _, a := range list.names {
			if !slices.Contains(actions, a) {
				return fail(fmt.Errorf("fairness: unknown action %q", a))
			}
			if seen[a] {
				return fail(fmt.Errorf("fairness: action %q is listed twice", a))
			}
			seen[a] = true
			assumptions = append(assumptions, fmt.Sprintf("%s(%s, %s)", list.kind, a, tuple))
		}
	}
	c := expr.Compiler{Records: m.spec.Records}
	scope := map[string]expr.Symbol{"event": {Type: "string", Quint: "event"}}
	for name, v := range m.spec.State {
		scope[name] = expr.Symbol{Type: v.Type, Quint: name}
	}
	for name := range m.spec.Domains {
		scope[name] = expr.Symbol{Type: expr.Domain, Quint: name}
	}
	predicate := func(field string, source expr.Expression) (string, error) {
		if source == "" {
			return "", fmt.Errorf("%s: predicate required", field)
		}
		s, err := c.Compile(source, scope, "bool")
		if err != nil {
			return "", fmt.Errorf("%s: %w", field, err)
		}
		return s.Quint, nil
	}
	var formula string
	switch {
	case t.Eventually != "" && t.LeadsTo != nil:
		return fail(fmt.Errorf("set one of eventually and leadsTo, not both"))
	case t.Eventually != "":
		p, err := predicate("eventually", t.Eventually)
		if err != nil {
			return fail(err)
		}
		formula = "eventually(" + p + ")"
	case t.LeadsTo != nil:
		from, err := predicate("leadsTo.from", t.LeadsTo.From)
		if err != nil {
			return fail(err)
		}
		to, err := predicate("leadsTo.to", t.LeadsTo.To)
		if err != nil {
			return fail(err)
		}
		formula = "always(" + from + " implies eventually(" + to + "))"
	default:
		return fail(fmt.Errorf("set one of eventually and leadsTo"))
	}
	// Checked last, so that a property without fairness is still
	// validated: ErrNoFairness then means the definition itself is sound.
	if fair.Empty() {
		return fail(ErrNoFairness)
	}
	src := m.quint
	end := strings.LastIndex(src, "}")
	if end < 0 {
		return fail(fmt.Errorf("model module is not closed"))
	}
	return fmt.Sprintf("%s temporal %s = (%s) implies %s\n%s", src[:end], t.Name, strings.Join(assumptions, " and "), formula, src[end:]), nil
}

// uses reports whether the model declares name or generates it.
func (m *Compiled) uses(name string) bool {
	if _, ok := m.spec.State[name]; ok {
		return true
	}
	if _, ok := m.spec.Records[name]; ok {
		return true
	}
	if _, ok := m.spec.Domains[name]; ok {
		return true
	}
	for _, a := range m.spec.Actions {
		if a.Name == name {
			return true
		}
	}
	for _, p := range m.spec.Properties {
		if p.Name == name {
			return true
		}
	}
	return false
}
