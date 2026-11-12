package composition

import (
	"fmt"
	"github.com/phoban01/generative-architecture/pkg/ir"
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
// See PortsFromIR for the rules; without watches no port is an owner.
func PortsFromEffects(component string, effects []ir.Effect) map[string]Port {
	return PortsFromIR(component, nil, effects)
}

// PortsFromIR derives ports from effects and watches. A watch reads the
// watched resource. Schema-only and external effects touch no resource at
// run time and yield no port.
//
// The provider of a resource is chosen by ownership, not by effect direction
// (ADR-0010). A resource that is the kind of a primary watch becomes an out
// port marked Owner. A resource the component creates or applies becomes an
// out port marked Creator. Any other resource becomes an in port, writable
// when the component writes it. ResolveProviders later turns a creator into
// a writable in port when another component owns the resource.
//
// Each derived port carries the strongest evidence of the facts behind it, so
// a port backed only by declared facts stays declared. It also carries their
// provenance, in watch and effect ID order.
func PortsFromIR(component string, watches []ir.Watch, effects []ir.Effect) map[string]Port {
	type use struct {
		write, owner, creator bool
		evidence              ir.Evidence
		sources               map[string]ir.Provenance
	}
	uses := map[ir.GVK]*use{}
	touch := func(g ir.GVK, id string, e ir.Evidence, p ir.Provenance) *use {
		u := uses[g]
		if u == nil {
			u = &use{evidence: e, sources: map[string]ir.Provenance{}}
			uses[g] = u
		}
		u.evidence = ir.Strongest(u.evidence, e)
		u.sources[id] = p
		return u
	}
	for _, w := range watches {
		u := touch(w.Resource, "watch/"+w.ID, w.Evidence, w.Provenance)
		u.owner = u.owner || w.Primary
	}
	for _, e := range effects {
		if e.External() || e.Consistency == ir.ConsistencySchema {
			continue
		}
		u := touch(e.Resource, "effect/"+e.ID, e.Evidence, e.Provenance)
		u.write = u.write || writeOps[e.Operation]
		u.creator = u.creator || e.Operation == ir.OpCreate || e.Operation == ir.OpApply
	}
	ports := map[string]Port{}
	for g, u := range uses {
		p := Port{State: stateName(g), Contract: ResourceContract(g), Version: ResourceContractVersion, Direction: "in", Writable: u.write, Owner: u.owner, Creator: u.creator, Evidence: u.evidence}
		if u.owner || u.creator {
			p.Direction, p.Writable = "out", false
		}
		for _, id := range sortedKeys(u.sources) {
			p.Provenance = append(p.Provenance, u.sources[id])
		}
		ports[portName(g)] = p
	}
	return ports
}

// ResolveProviders applies the ownership rule of ADR-0010 across components.
// It groups resource out ports by cluster, contract and version. An owner
// port, or a hand-written or boundary out port, owns the resource. When one
// exists, every creator that is not an owner becomes a writable in port, so
// the owner provides. When none exists, creators stay out ports: one creator
// provides, and several stay ambiguous for JoinByResource. The result is a
// copy; the input is not changed. Applying it twice gives the same result.
func ResolveProviders(components []Component, cluster map[string]ir.ClusterID) []Component {
	owned := map[string]bool{}
	key := func(c Component, p Port) string {
		return string(cluster[c.Name]) + "\x00" + p.Contract + "/" + p.Version
	}
	for _, c := range components {
		for _, p := range c.Ports {
			if p.Direction == "out" && strings.HasPrefix(p.Contract, ResourceContractPrefix) && (p.Owner || !p.Creator) {
				owned[key(c, p)] = true
			}
		}
	}
	out := make([]Component, len(components))
	for i, c := range components {
		out[i] = c
		out[i].Ports = make(map[string]Port, len(c.Ports))
		for name, p := range c.Ports {
			if p.Direction == "out" && p.Creator && !p.Owner && owned[key(c, p)] {
				p.Direction, p.Writable = "in", true
			}
			out[i].Ports[name] = p
		}
	}
	return out
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
