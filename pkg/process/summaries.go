package process

import (
	"fmt"

	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/summaries"
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

// summarize derives the variants of a node that has no hand-written
// operation: the built-in summary of the node's kind (its operation key,
// see summaries.OperationKey) at the node's consistency, instantiated with
// the node's bindings. Every variant is labelled with the node's effect.
func (c Config) summarize(n goflow.Node) ([]Variant, error) {
	s, err := c.Summaries.Lookup(n.Kind, n.Consistency)
	if err != nil {
		return nil, err
	}
	variants, err := VariantsFor(s, n.Bindings)
	if err != nil {
		return nil, fmt.Errorf("summary %s/%s: %w", s.Operation, s.Consistency, err)
	}
	for i := range variants {
		variants[i].Effect = n.Effect
	}
	return variants, nil
}
