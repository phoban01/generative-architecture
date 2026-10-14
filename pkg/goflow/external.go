package goflow

import (
	"fmt"

	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/krm"
)

// ExternalEffectsKind is the KRM kind of a document declaring the effects a
// controller performs outside the Kubernetes API (ARCH-13). The internals of
// such a call are never extracted: the document names the callee symbol and
// declares its outcomes, and typed extraction lowers a matching call to a
// node of kind effect.
const ExternalEffectsKind = "ExternalEffects"

// KindEffect is the node kind of a call lowered to a declared external
// effect. The node's Effect field holds the effect identifier.
const KindEffect = "effect"

// ExternalEffects is the spec of an ExternalEffects document.
type ExternalEffects struct {
	Effects []ExternalEffect `yaml:"effects"`
}

// ExternalEffect declares one external effect: its identifier, the symbol
// whose calls perform it and the outcomes those calls can have. Actor is the
// controller performing it and may be left to the frontend that knows which
// controller it extracts.
type ExternalEffect struct {
	ID       string       `yaml:"id"`
	Actor    string       `yaml:"actor,omitempty"`
	Target   ir.Callee    `yaml:"target"`
	Outcomes []ir.Outcome `yaml:"outcomes"`
}

// CalleeOf converts an IR callee to the symbol form typed extraction
// resolves calls to.
func CalleeOf(c ir.Callee) Callee {
	return Callee{Package: c.Package, Receiver: c.Receiver, Method: c.Func}
}

// Resolve validates the declarations and keys them by callee for
// Options.External. Each effect becomes an IR effect of kind external with
// declared evidence; identifiers and targets must be unique.
func (d ExternalEffects) Resolve() (map[Callee]ir.Effect, error) {
	out := make(map[Callee]ir.Effect, len(d.Effects))
	ids := map[string]bool{}
	for i, e := range d.Effects {
		if e.ID == "" {
			return nil, fmt.Errorf("external effect at index %d has no id", i)
		}
		if ids[e.ID] {
			return nil, fmt.Errorf("external effect %s declared twice", e.ID)
		}
		ids[e.ID] = true
		effect := ir.Effect{ID: e.ID, Kind: ir.KindExternal, Actor: e.Actor, Target: &e.Target, Outcomes: e.Outcomes, Evidence: ir.Declared}
		if err := validateExternal(effect); err != nil {
			return nil, err
		}
		c := CalleeOf(e.Target)
		if prior, dup := out[c]; dup {
			return nil, fmt.Errorf("external effects %s and %s declare the same target %s", prior.ID, e.ID, e.Target.String())
		}
		out[c] = effect
	}
	return out, nil
}

// validateExternal applies the IR rules for an external effect. The actor is
// the only field a document may leave for the frontend to fill, so it is
// validated with a placeholder when absent.
func validateExternal(e ir.Effect) error {
	if e.Actor == "" {
		e.Actor = "-"
	}
	return e.Validate()
}

// ReadExternalEffects loads an ExternalEffects document and resolves it.
func ReadExternalEffects(path string) (map[Callee]ir.Effect, error) {
	var doc ExternalEffects
	if _, err := krm.Read(path, ExternalEffectsKind, &doc); err != nil {
		return nil, err
	}
	effects, err := doc.Resolve()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return effects, nil
}

// ExternalByID re-keys resolved external effects by identifier, the form the
// process lowering consults for a node of kind effect.
func ExternalByID(effects map[Callee]ir.Effect) map[string]ir.Effect {
	out := make(map[string]ir.Effect, len(effects))
	for _, e := range effects {
		out[e.ID] = e
	}
	return out
}

// externalCall lowers a call to a declared external effect. The node is of
// kind effect and names the effect; arguments are not lowered because the
// effect's internals are declared rather than extracted.
func (e *extractor) externalCall(c Callee) (Node, bool) {
	effect, ok := e.opts.External[c]
	if !ok {
		return Node{}, false
	}
	return Node{Kind: KindEffect, Effect: effect.ID}, true
}

// String renders the callee as package.Receiver.Method, or package.Method for
// a function.
func (c Callee) String() string {
	if c.Receiver == "" {
		return c.Package + "." + c.Method
	}
	return c.Package + "." + c.Receiver + "." + c.Method
}
