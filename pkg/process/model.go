// Package process lowers source-extracted control flow and declared effect
// semantics into the shared transition model. It has no domain/runtime imports.
package process

import (
	"fmt"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/summaries"
	"strconv"
)

// Variant is a possible outcome of an external effect. Guards/updates are CEL.
// _value refers to the expression extracted from the actual Go statement.
type Variant struct {
	Name    string                     `yaml:"name"`
	Guard   expr.Expression            `yaml:"guard"`
	Updates map[string]expr.Expression `yaml:"updates"`
	Effect  string                     `yaml:"effect,omitempty"`
	// Error is the error class the outcome reports (see summaries); empty
	// means success. It is recorded in the node's error symbol.
	Error string `yaml:"error,omitempty"`
}

// Config is the payload of a KRM ProcessSemantics document plus the
// libraries Expand consults for nodes it does not describe by hand.
// Operations are hand-written variants keyed by node kind (or, for a node
// of kind effect, by effect identifier) and always win; otherwise a node of
// kind effect is expanded from the declared outcomes of its External effect
// and a node of any other kind from the built-in summary of that operation
// (see summarize).
type Config struct {
	PC         string               `yaml:"pc"`
	Finished   string               `yaml:"finished"`
	Results    []string             `yaml:"results"`
	Operations map[string][]Variant `yaml:"operations"`
	Summaries  summaries.Registry   `yaml:"-"`
	External   map[string]ir.Effect `yaml:"-"`
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
			key := n.Kind
			if n.Kind == goflow.KindEffect {
				key = n.Effect
			}
			variants = c.Operations[key]
			if len(variants) == 0 {
				var err error
				if variants, err = c.summarize(n); err != nil {
					return base, fmt.Errorf("no semantics for process operation %q at node %d: %w", n.Kind, n.ID, err)
				}
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
			if err := recordError(base.State, n, v, u); err != nil {
				return base, err
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

// recordError sets the error symbol a node binds its call's error to (see
// goflow.Node.Error): true for an outcome with an error class, false for a
// success. Outcome symbols the frontend declared for error predicates, such
// as err_notFound for apierrors.IsNotFound(err), are set when they are
// model state. The error symbol itself must be model state, so that no
// failure is dropped silently.
func recordError(state map[string]model.Variable, n goflow.Node, v Variant, u map[string]expr.Expression) error {
	if n.Error == "" || n.Kind == "branch" || n.Kind == "return" || n.Kind == "assign" {
		return nil
	}
	name := string(n.Error)
	if s, ok := state[name]; !ok || s.Type != "bool" {
		return fmt.Errorf("node %d binds its error to %s, which is not a bool state variable", n.ID, name)
	}
	if _, set := u[name]; set {
		return fmt.Errorf("node %d: outcome %s updates its error symbol %s directly", n.ID, v.Name, name)
	}
	u[name] = expr.Expression(strconv.FormatBool(v.Error != ""))
	for _, class := range []string{summaries.ErrorNotFound, summaries.ErrorAlreadyExists, summaries.ErrorConflict, summaries.ErrorTransient} {
		if s, ok := state[name+"_"+class]; ok && s.Type == "bool" {
			u[name+"_"+class] = expr.Expression(strconv.FormatBool(v.Error == class))
		}
	}
	return nil
}
