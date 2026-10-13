// Package model is a domain-neutral, typed transition model. It has no Kubernetes dependencies.
package model

import (
	"fmt"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/expr"
	"regexp"
	"sort"
	"strings"
)

type Variable struct {
	Type    string          `yaml:"type"`
	Initial expr.Expression `yaml:"initial"`
}
type Action struct {
	Name    string                     `yaml:"name"`
	Guard   expr.Expression            `yaml:"guard"`
	Updates map[string]expr.Expression `yaml:"updates"`
	Effect  string                     `yaml:"effect,omitempty"`
}
type Property struct {
	Name       string          `yaml:"name"`
	Expression expr.Expression `yaml:"expression"`
}

// Spec is the payload of a KRM TransitionModel. Actions update variables
// simultaneously. Unmentioned variables stutter. Effects label observation
// boundaries; their state semantics are explicitly supplied by the frontend.
type Spec struct {
	Name       string                       `yaml:"name"`
	Records    map[string]map[string]string `yaml:"records,omitempty"`
	State      map[string]Variable          `yaml:"state"`
	Effects    []effect.Descriptor          `yaml:"effects,omitempty"`
	Actions    []Action                     `yaml:"actions"`
	Properties []Property                   `yaml:"properties"`
}
type Compiled struct {
	spec     Spec
	quint    string
	registry *effect.Registry
}

func (m *Compiled) Quint() string             { return m.quint }
func (m *Compiled) Effects() *effect.Registry { return m.registry }

var ident = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
var reserved = map[string]bool{"init": true, "step": true, "safety": true, "event": true, "module": true, "type": true, "var": true, "val": true, "def": true, "action": true, "pure": true, "if": true, "else": true, "all": true, "any": true, "true": true, "false": true, "and": true, "or": true, "not": true, "str": true, "int": true, "bool": true, "string": true}

func names[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func Compile(s Spec) (*Compiled, error) {
	if !ident.MatchString(s.Name) || reserved[s.Name] || len(s.State) == 0 || len(s.Actions) == 0 {
		return nil, fmt.Errorf("model needs a valid name, state and actions")
	}
	c := expr.Compiler{Records: s.Records}
	scope := map[string]expr.Symbol{}
	used := map[string]bool{}
	checkName := func(name string) error {
		if !ident.MatchString(name) || reserved[name] || used[name] {
			return fmt.Errorf("invalid or duplicate model identifier %q", name)
		}
		used[name] = true
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "// Generated from a typed TransitionModel; declared semantics, not inferred intent.\nmodule %s {\n", s.Name)
	for _, name := range names(s.Records) {
		if err := checkName(name); err != nil {
			return nil, err
		}
		fields := []string{}
		if len(s.Records[name]) == 0 {
			return nil, fmt.Errorf("empty record %s", name)
		}
		for _, field := range names(s.Records[name]) {
			if !ident.MatchString(field) || reserved[field] {
				return nil, fmt.Errorf("invalid field %q", field)
			}
			// Keep the first public schema bounded: flat scalar records only.
			t := s.Records[name][field]
			if t != "string" && t != "int" && t != "bool" {
				return nil, fmt.Errorf("record %s: nested/non-scalar fields unsupported", name)
			}
			qt, err := c.QuintType(t)
			if err != nil {
				return nil, err
			}
			fields = append(fields, field+": "+qt)
		}
		fmt.Fprintf(&b, " type %s = { %s }\n", name, strings.Join(fields, ", "))
	}
	for _, name := range names(s.State) {
		if err := checkName(name); err != nil {
			return nil, err
		}
		v := s.State[name]
		qt, err := c.QuintType(v.Type)
		if err != nil {
			return nil, err
		}
		scope[name] = expr.Symbol{Type: v.Type, Quint: name}
		fmt.Fprintf(&b, " var %s: %s\n", name, qt)
	}
	fmt.Fprintln(&b, " var event: str")
	initial := []string{`event' = "init"`}
	for _, name := range names(s.State) {
		v := s.State[name]
		e, err := c.Compile(v.Initial, nil, v.Type)
		if err != nil {
			return nil, fmt.Errorf("initial %s: %w", name, err)
		}
		initial = append(initial, name+"' = "+e.Quint)
	}
	fmt.Fprintf(&b, " action init = all { %s }\n", strings.Join(initial, ", "))
	registry, err := effect.NewRegistry(s.Effects)
	if err != nil {
		return nil, err
	}
	effects := map[string]bool{}
	for _, e := range s.Effects {
		effects[e.ID] = true
	}
	actions := []string{}
	for _, a := range s.Actions {
		if err := checkName(a.Name); err != nil {
			return nil, err
		}
		if a.Effect != "" && !effects[a.Effect] {
			return nil, fmt.Errorf("action %s: unknown effect %s", a.Name, a.Effect)
		}
		guard, err := c.Compile(a.Guard, scope, "bool")
		if err != nil {
			return nil, fmt.Errorf("guard %s: %w", a.Name, err)
		}
		for name := range a.Updates {
			if _, ok := s.State[name]; !ok {
				return nil, fmt.Errorf("unknown update %s", name)
			}
		}
		body := []string{guard.Quint, fmt.Sprintf("event' = %q", a.Name)}
		for _, name := range names(s.State) {
			value := name
			if update, ok := a.Updates[name]; ok {
				v, err := c.Compile(update, scope, s.State[name].Type)
				if err != nil {
					return nil, fmt.Errorf("%s.%s: %w", a.Name, name, err)
				}
				value = v.Quint
			}
			body = append(body, name+"' = "+value)
		}
		fmt.Fprintf(&b, " action %s = all { %s }\n", a.Name, strings.Join(body, ", "))
		actions = append(actions, a.Name)
	}
	fmt.Fprintf(&b, " action step = any { %s }\n", strings.Join(actions, ", "))
	scope["event"] = expr.Symbol{Type: "string", Quint: "event"}
	props := []string{}
	for _, p := range s.Properties {
		if err := checkName(p.Name); err != nil {
			return nil, err
		}
		v, err := c.Compile(p.Expression, scope, "bool")
		if err != nil {
			return nil, fmt.Errorf("property %s: %w", p.Name, err)
		}
		fmt.Fprintf(&b, " val %s = %s\n", p.Name, v.Quint)
		props = append(props, p.Name)
	}
	if len(props) == 0 {
		props = []string{"true"}
	}
	fmt.Fprintf(&b, " val safety = %s\n}\n", strings.Join(props, " and "))
	// Defensive deep copy: callers cannot change replay semantics after compilation.
	s.State = cloneState(s.State)
	s.Records = cloneRecords(s.Records)
	s.Effects = append([]effect.Descriptor(nil), s.Effects...)
	s.Properties = append([]Property(nil), s.Properties...)
	actionsCopy := make([]Action, len(s.Actions))
	for i, a := range s.Actions {
		u := map[string]expr.Expression{}
		for k, v := range a.Updates {
			u[k] = v
		}
		a.Updates = u
		actionsCopy[i] = a
	}
	s.Actions = actionsCopy
	return &Compiled{spec: s, quint: b.String(), registry: registry}, nil
}
func cloneState(in map[string]Variable) map[string]Variable {
	out := map[string]Variable{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneRecords(in map[string]map[string]string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for k, v := range in {
		out[k] = map[string]string{}
		for f, t := range v {
			out[k][f] = t
		}
	}
	return out
}

func (m *Compiled) Initial() (map[string]any, error) {
	e, err := expr.NewEvaluator()
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, name := range names(m.spec.State) {
		v, err := e.Eval(m.spec.State[name].Initial, nil)
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
	return out, nil
}

// Next evaluates original CEL independently of the Quint lowering.
func (m *Compiled) Next(state map[string]any, action string) (map[string]any, error) {
	e, err := expr.NewEvaluator()
	if err != nil {
		return nil, err
	}
	for _, a := range m.spec.Actions {
		if a.Name != action {
			continue
		}
		enabled, err := e.Eval(a.Guard, state)
		if err != nil {
			return nil, err
		}
		if enabled != true {
			return nil, fmt.Errorf("action %s disabled", action)
		}
		out := map[string]any{}
		for k, v := range state {
			out[k] = v
		}
		for name, update := range a.Updates {
			v, err := e.Eval(update, state)
			if err != nil {
				return nil, err
			}
			out[name] = v
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown action %s", action)
}
func (m *Compiled) Check(state map[string]any, event string) error {
	e, err := expr.NewEvaluator()
	if err != nil {
		return err
	}
	vars := map[string]any{}
	for k, v := range state {
		vars[k] = v
	}
	vars["event"] = event
	for _, p := range m.spec.Properties {
		v, err := e.Eval(p.Expression, vars)
		if err != nil {
			return err
		}
		if v != true {
			return fmt.Errorf("property %s violated", p.Name)
		}
	}
	return nil
}
