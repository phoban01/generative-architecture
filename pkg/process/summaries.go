package process

import (
	"fmt"

	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/summaries"
)

// VariantsFor instantiates every outcome of a summary with the bindings and
// returns them as variants in declared order. Bindings map each $placeholder
// the summary uses to a model expression, or for an update target to a model
// variable. An unbound placeholder is an error so that no outcome is dropped
// silently; the receiving model compiler checks the substituted CEL.
func VariantsFor(s summaries.Summary, bindings map[string]string) ([]Variant, error) {
	out := make([]Variant, 0, len(s.Outcomes))
	for _, o := range s.Outcomes {
		bound, err := o.Instantiate(bindings)
		if err != nil {
			return nil, err
		}
		v := Variant{Name: bound.Name, Guard: expr.Expression(bound.Guard)}
		if len(bound.Updates) > 0 {
			v.Updates = make(map[string]expr.Expression, len(bound.Updates))
		}
		for target, value := range bound.Updates {
			v.Updates[target] = expr.Expression(value)
		}
		out = append(out, v)
	}
	return out, nil
}

// ExternalSummary presents the declared outcomes of an external effect as a
// summary, so they expand through VariantsFor like a built-in operation.
// Outcomes keep their declared order; an outcome without a guard is always
// enabled, as the declaration leaves the choice to the environment.
func ExternalSummary(e ir.Effect) (summaries.Summary, error) {
	if !e.External() {
		return summaries.Summary{}, fmt.Errorf("effect %s is not external", e.ID)
	}
	if len(e.Outcomes) == 0 {
		return summaries.Summary{}, fmt.Errorf("external effect %s declares no outcomes", e.ID)
	}
	s := summaries.Summary{Operation: e.ID, Consistency: ir.KindExternal}
	for _, o := range e.Outcomes {
		if o.Name == "" {
			return summaries.Summary{}, fmt.Errorf("external effect %s: outcome without a name", e.ID)
		}
		out := summaries.Outcome{Name: o.Name, Guard: o.Guard, Updates: o.Updates}
		if o.Error {
			// A declared failure carries no class; it is reported as an
			// unspecific error.
			out.Error = summaries.ErrorTransient
		}
		if out.Guard == "" {
			out.Guard = "true"
		}
		s.Outcomes = append(s.Outcomes, out)
	}
	return s, nil
}

// summarize derives the variants of a node that has no hand-written
// operation. A node of kind effect expands the declared outcomes of the
// External effect it names; any other node expands the built-in summary of
// its kind (an operation key, see summaries.OperationKey) at its
// consistency. Both instantiate placeholders from the node's bindings.
// Variants of a client operation are labelled with the node's effect so the
// model can gate them; a declared external effect has no model descriptor
// yet and is left unlabelled.
func (c Config) summarize(n goflow.Node) ([]Variant, error) {
	var s summaries.Summary
	label := n.Effect
	switch n.Kind {
	case goflow.KindEffect:
		e, ok := c.External[n.Effect]
		if !ok {
			return nil, fmt.Errorf("undeclared external effect %q", n.Effect)
		}
		var err error
		if s, err = ExternalSummary(e); err != nil {
			return nil, err
		}
		label = ""
	default:
		var err error
		if s, err = c.Summaries.Lookup(n.Kind, n.Consistency); err != nil {
			return nil, err
		}
	}
	variants, err := VariantsFor(s, n.Bindings)
	if err != nil {
		return nil, fmt.Errorf("summary %s/%s: %w", s.Operation, s.Consistency, err)
	}
	for i := range variants {
		variants[i].Effect = label
		variants[i].Error = s.Outcomes[i].Error
	}
	return variants, nil
}
