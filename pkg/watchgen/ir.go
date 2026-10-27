package watchgen

import (
	"strings"

	"generative-architecture/pkg/ir"
)

// Generator names this frontend in provenance records.
const Generator = "watchgen"

// IR converts the extracted rules into IR watch records for the named
// controller. An empty controller falls back to the name the builder gave with
// Named. Each watch carries extracted evidence and a provenance record
// pointing at the builder call in file; its ID is the stable content hash.
func (r Result) IR(controller, file string) []ir.Watch {
	if controller == "" {
		controller = r.Name
	}
	out := make([]ir.Watch, 0, len(r.Rules))
	for i, rule := range r.Rules {
		mapping := ir.KeyMapping{Kind: ir.MappingSelf}
		switch rule.Mapping {
		case "self":
		case "owner":
			mapping = ir.KeyMapping{Kind: ir.MappingOwner, OnlyController: rule.OnlyController}
			if rule.Owner != nil {
				mapping.OwnerGVK = &ir.GVK{Group: rule.Owner.Group, Version: rule.Owner.Version, Kind: rule.Owner.Kind}
			}
		default:
			mapping = ir.KeyMapping{Kind: ir.MappingField, FieldPath: append([]string(nil), rule.NamePath...)}
		}
		predicates := make([]ir.Predicate, 0, len(rule.Predicates))
		for _, p := range rule.Predicates {
			predicates = append(predicates, irPredicate(p))
		}
		w := ir.Watch{
			Controller: controller,
			Resource:   ir.GVK{Group: rule.Resource.Group, Version: rule.Resource.Version, Kind: rule.Resource.Kind},
			Primary:    i == 0 && rule.Mapping == "self",
			Predicates: predicates,
			Mapping:    mapping,
			Evidence:   ir.Extracted,
			Provenance: ir.Provenance{Generator: Generator, Source: ir.Source{File: file, Line: rule.Line, SHA256: r.SourceSHA256}},
		}
		w.ID = w.StableID()
		out = append(out, w)
	}
	return out
}

// irPredicate converts a predicate summary into an IR predicate. A composed
// expression such as or(a, not(b)) keeps its operator as the kind and the
// whole expression as an argument.
func irPredicate(p string) ir.Predicate {
	if i := strings.IndexByte(p, '('); i > 0 && strings.HasSuffix(p, ")") {
		return ir.Predicate{Kind: p[:i], Args: map[string]string{"expression": p}}
	}
	return ir.Predicate{Kind: p}
}
