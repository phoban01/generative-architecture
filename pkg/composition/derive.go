package composition

import (
	"fmt"
	"generative-architecture/pkg/ir"
	"sort"
	"strings"
)

// ResourceContractPrefix marks a port contract that names a Kubernetes
// resource rather than an authored interface.
const ResourceContractPrefix = "resource:"

// ResourceContractVersion is the contract version of every derived port.
const ResourceContractVersion = "v1"

var writeOps = map[string]bool{ir.OpCreate: true, ir.OpUpdate: true, ir.OpPatch: true, ir.OpDelete: true, ir.OpDeleteAllOf: true, ir.OpApply: true}

// ResourceContract names the port contract of a resource:
// resource:<group>/<version>/<kind>. The core group is empty.
func ResourceContract(g ir.GVK) string {
	return ResourceContractPrefix + g.Group + "/" + g.Version + "/" + g.Kind
}

// Diagnostic is a located finding about ports or joins. Port names the
// component and port it is about. Diagnostics do not stop derivation; the
// caller decides whether to fail.
type Diagnostic struct {
	Code       string   `yaml:"code"`
	Port       string   `yaml:"port"`
	Message    string   `yaml:"message"`
	Candidates []string `yaml:"candidates,omitempty"`
}

// Diagnostic codes.
const (
	DiagDuplicatePort      = "duplicate port"
	DiagUnresolvedProvider = "unresolved provider"
	DiagAmbiguousProvider  = "ambiguous provider"
)

func (d Diagnostic) String() string {
	s := d.Port + ": " + d.Code + ": " + d.Message
	if len(d.Candidates) > 0 {
		s += " (candidates: " + strings.Join(d.Candidates, ", ") + ")"
	}
	return s
}

// PortsFromEffects derives the ports of one component from its api effects.
// A resource the component only reads becomes a read-only in port. A
// resource it only writes becomes an out port. A resource it reads and
// writes becomes one writable in port. Schema-only and external effects
// touch no resource at run time and yield no port.
func PortsFromEffects(component string, effects []ir.Effect) map[string]Port {
	return PortsFromIR(component, nil, effects)
}

// PortsFromIR derives ports from effects and watches. A watch reads the
// watched resource. Each derived port carries the strongest evidence of the
// facts behind it, so a port backed only by declared facts stays declared.
// It also carries their provenance, in watch and effect ID order.
func PortsFromIR(component string, watches []ir.Watch, effects []ir.Effect) map[string]Port {
	type use struct {
		read, write bool
		evidence    ir.Evidence
		sources     map[string]ir.Provenance
	}
	uses := map[ir.GVK]*use{}
	touch := func(g ir.GVK, id string, read bool, e ir.Evidence, p ir.Provenance) {
		u := uses[g]
		if u == nil {
			u = &use{evidence: e, sources: map[string]ir.Provenance{}}
			uses[g] = u
		}
		u.read = u.read || read
		u.write = u.write || !read
		u.evidence = ir.Strongest(u.evidence, e)
		u.sources[id] = p
	}
	for _, w := range watches {
		touch(w.Resource, "watch/"+w.ID, true, w.Evidence, w.Provenance)
	}
	for _, e := range effects {
		if e.External() || e.Consistency == ir.ConsistencySchema {
			continue
		}
		touch(e.Resource, "effect/"+e.ID, !writeOps[e.Operation], e.Evidence, e.Provenance)
	}
	ports := map[string]Port{}
	for g, u := range uses {
		p := Port{State: stateName(g), Contract: ResourceContract(g), Version: ResourceContractVersion, Direction: "in", Writable: u.read && u.write, Evidence: u.evidence}
		if !u.read {
			p.Direction = "out"
		}
		for _, id := range sortedKeys(u.sources) {
			p.Provenance = append(p.Provenance, u.sources[id])
		}
		ports[portName(g)] = p
	}
	return ports
}

// MergePorts adds declared ports to derived ones. A declared port that reuses
// a derived port's name or contract duplicates it. The derived port is kept
// and the declared one is reported, never merged.
func MergePorts(component string, declared, derived map[string]Port) (map[string]Port, []Diagnostic) {
	out := map[string]Port{}
	contracts := map[string]string{}
	for name, p := range derived {
		out[name] = p
		contracts[p.Contract+"/"+p.Version] = name
	}
	var diags []Diagnostic
	for _, name := range sortedKeys(declared) {
		p := declared[name]
		other, sameName := derived[name]
		dup, sameContract := contracts[p.Contract+"/"+p.Version]
		switch {
		case sameName:
			diags = append(diags, Diagnostic{Code: DiagDuplicatePort, Port: component + "." + name, Message: fmt.Sprintf("declared port reuses the name of the port derived for %s; remove the declaration", other.Contract)})
		case sameContract:
			diags = append(diags, Diagnostic{Code: DiagDuplicatePort, Port: component + "." + name, Message: fmt.Sprintf("declared port duplicates derived port %s.%s for %s/%s; remove the declaration", component, dup, p.Contract, p.Version)})
		default:
			out[name] = p
		}
	}
	return out, diags
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// portName is a model identifier unique per GVK: kind, version, then group.
func portName(g ir.GVK) string {
	parts := []string{strings.ToLower(g.Kind), g.Version}
	if g.Group != "" {
		parts = append(parts, g.Group)
	}
	return identifier(strings.Join(parts, "_"))
}

// stateName is <kind>_objects, the state variable in which a component
// models the objects of the resource.
func stateName(g ir.GVK) string {
	return identifier(strings.ToLower(g.Kind)) + "_objects"
}

func identifier(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			b[i] = '_'
		}
	}
	return string(b)
}
