package dynamicgen

import (
	"strings"

	"generative-architecture/pkg/ir"
)

// Generator names this frontend in provenance records.
const Generator = "dynamicgen"

// IR converts the extracted accesses into IR api effects of controller, so
// pkg/likec4 can draw them with ResourceView. Each effect takes the ID of
// the declared effect its method maps to, direct consistency (the dynamic
// client reads through the API server) and extracted evidence, and points
// at the access in file. An operation such as status.update is split into
// subresource and verb.
func (r Result) IR(controller, file string) []ir.Effect {
	out := make([]ir.Effect, 0, len(r.Accesses))
	for _, a := range r.Accesses {
		res := a.Binding.Resource
		e := ir.Effect{
			ID: a.Effect, Kind: ir.KindAPI, Actor: controller,
			Resource:  ir.GVK{Group: res.Group, Version: res.Version, Kind: res.Kind},
			Operation: a.Operation, Consistency: ir.ConsistencyDirect,
			Evidence:   ir.Extracted,
			Provenance: ir.Provenance{Generator: Generator, Source: ir.Source{File: file, Line: a.Line, SHA256: r.SourceSHA256}},
		}
		if sub, verb, ok := strings.Cut(a.Operation, "."); ok {
			e.Subresource, e.Operation = sub, verb
		}
		out = append(out, e)
	}
	return out
}
