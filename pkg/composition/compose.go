// Package composition binds independent models through checked, versioned ports.
package composition

import (
	"fmt"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/model"
	"reflect"
	"strings"
)

type Port struct {
	State     string `yaml:"state"`
	Contract  string `yaml:"contract"`
	Version   string `yaml:"version"`
	Direction string `yaml:"direction"`
	Writable  bool   `yaml:"writable,omitempty"`
	// Evidence and Provenance are set on ports derived from IR facts. A
	// hand-written port leaves them empty and counts as declared.
	Evidence   ir.Evidence     `yaml:"evidence,omitempty"`
	Provenance []ir.Provenance `yaml:"provenance,omitempty"`
}
type Component struct {
	Name  string
	Model model.Spec
	Ports map[string]Port
}
type Link struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}
type Result struct {
	Model       model.Spec
	StateNames  map[string]string
	ActionNames map[string]string
}

// Compose namespaces private state and aliases bound ports to one shared state
// location. Reads observe that shared state; snapshots remain private locals.
func Compose(name string, components []Component, links []Link, properties []model.Property) (Result, error) {
	result := Result{Model: model.Spec{Name: name, Records: map[string]map[string]string{}, State: map[string]model.Variable{}, Properties: append([]model.Property(nil), properties...)}, StateNames: map[string]string{}, ActionNames: map[string]string{}}
	generatedNames := map[string]string{}
	byName := map[string]Component{}
	ports := map[string]Port{}
	aliases := map[string]string{}
	for _, c := range components {
		if c.Name == "" || byName[c.Name].Name != "" {
			return result, fmt.Errorf("duplicate/empty component")
		}
		if _, err := model.Compile(c.Model); err != nil {
			return result, fmt.Errorf("component %s: %w", c.Name, err)
		}
		byName[c.Name] = c
		for variable := range c.Model.State {
			full := c.Name + "." + variable
			generated := c.Name + "_" + variable
			if old, ok := generatedNames[generated]; ok {
				return result, fmt.Errorf("namespace collision: %s and %s", old, full)
			}
			generatedNames[generated] = full
			result.StateNames[full] = generated
		}
		for n, p := range c.Ports {
			if p.Contract == "" || p.Version == "" || (p.Direction != "in" && p.Direction != "out") {
				return result, fmt.Errorf("incomplete port %s.%s", c.Name, n)
			}
			if _, ok := c.Model.State[p.State]; !ok {
				return result, fmt.Errorf("unknown port state %s", p.State)
			}
			ports[c.Name+"."+n] = p
		}
	}
	targetStates := map[string]bool{}
	for _, l := range links {
		from, ok := ports[l.From]
		if !ok {
			return result, fmt.Errorf("unknown source port %s", l.From)
		}
		to, ok := ports[l.To]
		if !ok {
			return result, fmt.Errorf("unknown target port %s", l.To)
		}
		if from.Direction != "out" || to.Direction != "in" || from.Contract != to.Contract || from.Version != to.Version {
			return result, fmt.Errorf("incompatible contracts: %s -> %s", l.From, l.To)
		}
		fn := strings.SplitN(l.From, ".", 2)[0]
		tn := strings.SplitN(l.To, ".", 2)[0]
		source := fn + "." + from.State
		target := tn + "." + to.State
		if targetStates[target] {
			return result, fmt.Errorf("multiple bindings for %s", target)
		}
		targetStates[target] = true
		f, t := byName[fn].Model.State[from.State], byName[tn].Model.State[to.State]
		if f.Type != t.Type {
			return result, fmt.Errorf("port type mismatch: %s -> %s", l.From, l.To)
		}
		eval, err := expr.NewEvaluator()
		if err != nil {
			return result, err
		}
		fv, err := eval.Eval(f.Initial, nil)
		if err != nil {
			return result, err
		}
		tv, err := eval.Eval(t.Initial, nil)
		if err != nil {
			return result, err
		}
		if !reflect.DeepEqual(fv, tv) {
			return result, fmt.Errorf("inconsistent shared initialization: %s -> %s", l.From, l.To)
		}
		aliases[target] = source
	}
	for full, p := range ports {
		component := strings.SplitN(full, ".", 2)[0]
		if p.Direction == "in" && !targetStates[component+"."+p.State] {
			return result, fmt.Errorf("unbound input %s", full)
		}
	}
	var resolve func(string, map[string]bool) (string, error)
	resolve = func(s string, seen map[string]bool) (string, error) {
		if seen[s] {
			return "", fmt.Errorf("cyclic state aliases")
		}
		seen[s] = true
		if next, ok := aliases[s]; ok {
			return resolve(next, seen)
		}
		return result.StateNames[s], nil
	}
	for full := range result.StateNames {
		v, err := resolve(full, map[string]bool{})
		if err != nil {
			return result, err
		}
		result.StateNames[full] = v
	}
	// Merge effect identities once, with actor-scoped lookups for multiple controllers.
	for _, c := range components {
		bindings := map[string]expr.Expression{}
		for state := range c.Model.State {
			bindings[state] = expr.Expression(result.StateNames[c.Name+"."+state])
		}
		for typ, fields := range c.Model.Records {
			if old, ok := result.Model.Records[typ]; ok && !reflect.DeepEqual(old, fields) {
				return result, fmt.Errorf("record schema collision: %s", typ)
			}
			result.Model.Records[typ] = fields
		}
		for state, v := range c.Model.State {
			full := c.Name + "." + state
			if _, bound := aliases[full]; !bound {
				result.Model.State[result.StateNames[full]] = v
			}
		}
		for _, e := range c.Model.Effects {
			e.ID = c.Name + "_" + e.ID
			e.Actor = c.Name
			result.Model.Effects = append(result.Model.Effects, e)
		}
		for _, a := range c.Model.Actions {
			original := a.Name
			a.Name = c.Name + "_" + a.Name
			result.ActionNames[c.Name+"."+original] = a.Name
			guard, err := expr.Rewrite(a.Guard, bindings)
			if err != nil {
				return result, err
			}
			a.Guard = guard
			updates := map[string]expr.Expression{}
			for variable, value := range a.Updates {
				for _, p := range c.Ports {
					if p.State == variable && p.Direction == "in" && !p.Writable {
						return result, fmt.Errorf("action %s writes read-only input %s", a.Name, variable)
					}
				}
				rewritten, err := expr.Rewrite(value, bindings)
				if err != nil {
					return result, err
				}
				target := result.StateNames[c.Name+"."+variable]
				if _, exists := updates[target]; exists {
					return result, fmt.Errorf("aliased simultaneous writes in %s", a.Name)
				}
				updates[target] = rewritten
			}
			a.Updates = updates
			if a.Effect != "" {
				a.Effect = c.Name + "_" + a.Effect
			}
			result.Model.Actions = append(result.Model.Actions, a)
		}
		// Local properties see a local action name even after action namespacing.
		cases := expr.Expression(`event == "init" ? "init" : ""`)
		for i := len(c.Model.Actions) - 1; i >= 0; i-- {
			a := c.Model.Actions[i]
			cases = expr.Expression(fmt.Sprintf("event == %q ? %q : (%s)", c.Name+"_"+a.Name, a.Name, cases))
		}
		bindings["event"] = cases
		for _, p := range c.Model.Properties {
			p.Name = c.Name + "_" + p.Name
			v, err := expr.Rewrite(p.Expression, bindings)
			if err != nil {
				return result, err
			}
			p.Expression = v
			result.Model.Properties = append(result.Model.Properties, p)
		}
	}
	systemBindings := map[string]expr.Expression{}
	for full, target := range result.StateNames {
		systemBindings[full] = expr.Expression(target)
	}
	for i := range properties {
		v, err := expr.Rewrite(result.Model.Properties[i].Expression, systemBindings)
		if err != nil {
			return result, err
		}
		result.Model.Properties[i].Expression = v
	}
	if _, err := effect.NewRegistry(result.Model.Effects); err != nil {
		return result, err
	}
	_, err := model.Compile(result.Model)
	return result, err
}
