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
// variable. Vectors map a placeholder to one variable per selected field of
// the addressed object: an update that names a vector placeholder is
// instantiated once per field, with the field's variable in place of every
// vector placeholder, so an object with several fields is read and written
// field by field. A guard may not name a vector placeholder. An unbound
// placeholder is an error so that no outcome is dropped silently; the
// receiving model compiler checks the substituted CEL.
func VariantsFor(s summaries.Summary, bindings map[string]string, vectors map[string][]string) ([]Variant, error) {
	width := 0
	for p, vars := range vectors {
		if width == 0 {
			width = len(vars)
		}
		if len(vars) == 0 || len(vars) != width {
			return nil, fmt.Errorf("vector %s binds %d variables; every vector of a node binds the same positive number", p, len(vars))
		}
	}
	out := make([]Variant, 0, len(s.Outcomes))
	for _, o := range s.Outcomes {
		for _, p := range summaries.Placeholders(o.Guard) {
			if _, ok := vectors[p]; ok {
				return nil, fmt.Errorf("outcome %s: guard names the per-field placeholder %s", o.Name, p)
			}
		}
		v := Variant{Name: o.Name}
		guard, err := summaries.Substitute(o.Guard, bindings)
		if err != nil {
			return nil, fmt.Errorf("outcome %s: %w", o.Name, err)
		}
		v.Guard = expr.Expression(guard)
		if len(o.Updates) > 0 {
			v.Updates = map[string]expr.Expression{}
		}
		for target, value := range o.Updates {
			instances := 1
			if vectorial(vectors, target, value) {
				instances = width
			}
			for i := 0; i < instances; i++ {
				b := bindings
				if instances > 1 || vectorial(vectors, target, value) {
					b = make(map[string]string, len(bindings)+len(vectors))
					for k, val := range bindings {
						b[k] = val
					}
					for k, vars := range vectors {
						b[k] = vars[i]
					}
				}
				t, err := summaries.Substitute(target, b)
				if err != nil {
					return nil, fmt.Errorf("outcome %s: %w", o.Name, err)
				}
				if _, dup := v.Updates[t]; dup {
					return nil, fmt.Errorf("outcome %s: targets alias %s", o.Name, t)
				}
				val, err := summaries.Substitute(value, b)
				if err != nil {
					return nil, fmt.Errorf("outcome %s: %w", o.Name, err)
				}
				v.Updates[t] = expr.Expression(val)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// vectorial reports whether an update's target or value names a vector
// placeholder.
func vectorial(vectors map[string][]string, target, value string) bool {
	for _, p := range summaries.Placeholders(target, value) {
		if _, ok := vectors[p]; ok {
			return true
		}
	}
	return false
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
	variants, err := VariantsFor(s, n.Bindings, n.Vectors)
	if err != nil {
		return nil, fmt.Errorf("summary %s/%s: %w", s.Operation, s.Consistency, err)
	}
	for i := range variants {
		variants[i].Effect = label
		variants[i].Error = s.Outcomes[i].Error
	}
	return variants, nil
}
