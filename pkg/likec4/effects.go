package likec4

import (
	"strings"

	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/ir"
)

// EffectsOf turns the effect descriptors of a model into IR api effects of
// actor, so ResourceView can draw them. Each effect carries evidence and
// source, the place the descriptors are declared. An operation of the form
// <subresource>.<verb>, such as status.update, is split into its
// subresource and verb.
func EffectsOf(actor string, descriptors []effect.Descriptor, evidence ir.Evidence, source ir.Source) []ir.Effect {
	out := make([]ir.Effect, 0, len(descriptors))
	for _, d := range descriptors {
		e := ir.Effect{
			ID: d.ID, Kind: ir.KindAPI, Actor: actor,
			Resource:  ir.GVK{Group: d.Resource.Group, Version: d.Resource.Version, Kind: d.Resource.Kind},
			Operation: d.Operation, Consistency: d.Consistency,
			Evidence: evidence, Provenance: ir.Provenance{Source: source},
		}
		if sub, verb, ok := strings.Cut(d.Operation, "."); ok {
			e.Subresource, e.Operation = sub, verb
		}
		out = append(out, e)
	}
	return out
}
