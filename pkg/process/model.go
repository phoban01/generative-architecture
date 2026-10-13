// Package process lowers source-extracted control flow and declared effect
// semantics into the shared transition model. It has no domain/runtime imports.
package process

import (
	"fmt"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/model"
	"strconv"
)

// Variant is a possible outcome of an external effect. Guards/updates are CEL.
// _value refers to the expression extracted from the actual Go statement.
type Variant struct {
	Name    string                     `yaml:"name"`
	Guard   expr.Expression            `yaml:"guard"`
	Updates map[string]expr.Expression `yaml:"updates"`
	Effect  string                     `yaml:"effect,omitempty"`
}
type Config struct {
	PC         string               `yaml:"pc"`
	Finished   string               `yaml:"finished"`
	Results    []string             `yaml:"results"`
	Operations map[string][]Variant `yaml:"operations"`
}

// Expand adds instruction transitions to a caller-supplied model. The caller
// declares locals, bounded input values, environment actions and requirements.
// It can instantiate/rebind this result with composition.Compose.
func Expand(base model.Spec, p goflow.Program, c Config) (model.Spec, error) {
	if c.PC == "" || c.Finished == "" || c.PC == c.Finished || len(p.Nodes) == 0 {
		return base, fmt.Errorf("process requires pc, finished and nodes")
	}
	if _, exists := base.State[c.PC]; exists {
		return base, fmt.Errorf("pc collides with state")
	}
	if _, exists := base.State[c.Finished]; exists {
		return base, fmt.Errorf("finished collides with state")
	}
	state := map[string]model.Variable{}
	for k, v := range base.State {
		state[k] = v
	}
	base.State = state
	base.Actions = append([]model.Action(nil), base.Actions...)
	base.State[c.PC] = model.Variable{Type: "int", Initial: expr.Expression(strconv.Itoa(p.Entry))}
	base.State[c.Finished] = model.Variable{Type: "bool", Initial: "false"}
	seen := map[int]bool{}
	for _, n := range p.Nodes {
		if n.ID < 0 || seen[n.ID] {
			return base, fmt.Errorf("duplicate/invalid process node")
		}
		seen[n.ID] = true
	}
	if !seen[p.Entry] {
		return base, fmt.Errorf("missing process entry")
	}
	for _, n := range p.Nodes {
		if n.Kind != "return" && (!seen[n.Next] || (n.Kind == "branch" && !seen[n.Else])) {
			return base, fmt.Errorf("invalid successor at node %d", n.ID)
		}
		variants := []Variant{}
		switch n.Kind {
		case "branch":
			variants = []Variant{{Name: "branch", Guard: "true", Updates: map[string]expr.Expression{c.PC: expr.Expression(fmt.Sprintf("(%s) ? %d : %d", n.Expression, n.Next, n.Else))}}}
		case "assign":
			variants = []Variant{{Name: "assign", Guard: "true", Updates: n.Updates}}
		case "return":
			if len(n.Results) != len(c.Results) {
				return base, fmt.Errorf("return mapping arity mismatch")
			}
			u := map[string]expr.Expression{c.Finished: "true"}
			for i, target := range c.Results {
				if target != "" {
					u[target] = n.Results[i]
				}
			}
			variants = []Variant{{Name: "return", Guard: "true", Updates: u}}
		default:
			variants = c.Operations[n.Kind]
			if len(variants) == 0 {
				return base, fmt.Errorf("no semantics for process operation %q", n.Kind)
			}
		}
		for _, v := range variants {
			u := map[string]expr.Expression{c.PC: expr.Expression(strconv.Itoa(n.Next))}
			for k, value := range v.Updates {
				if (k == c.PC || k == c.Finished) && n.Kind != "branch" && n.Kind != "return" {
					return base, fmt.Errorf("operation may not override process control")
				}
				rewritten, err := expr.Rewrite(value, map[string]expr.Expression{"_value": n.Expression})
				if err != nil {
					return base, err
				}
				u[k] = rewritten
			}
			guard := v.Guard
			if guard == "" {
				return base, fmt.Errorf("operation guard required")
			}
			base.Actions = append(base.Actions, model.Action{Name: fmt.Sprintf("node%d_%s", n.ID, v.Name), Guard: expr.Expression(fmt.Sprintf("!%s && %s == %d && (%s)", c.Finished, c.PC, n.ID, guard)), Updates: u, Effect: v.Effect})
		}
	}
	if _, err := model.Compile(base); err != nil {
		return base, err
	}
	return base, nil
}
