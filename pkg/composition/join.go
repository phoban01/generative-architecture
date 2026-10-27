package composition

import (
	"fmt"
	"generative-architecture/pkg/ir"
	"sort"
	"strings"
)

// JoinByResource joins each in port that carries a resource contract to the
// out port of the same contract and version in the same cluster. cluster maps
// a component name to its cluster; a missing entry is the implicit cluster.
// Two objects of one GVK in different clusters are distinct (ARCH-3), so
// candidates never cross clusters.
//
// One candidate yields a link. No candidate yields an "unresolved provider"
// diagnostic. Several candidates yield an "ambiguous provider" diagnostic
// that lists them and asks for one explicit binding. An explicit link to an
// input suppresses both diagnostics for it. The returned links are the
// explicit links followed by the joined ones. A link is connectivity only; it
// says nothing about protocol conformance.
//
// The error reports malformed input: an unknown component in cluster, an
// explicit link to an unknown port, or an explicit resource link across
// clusters.
func JoinByResource(components []Component, cluster map[string]ir.ClusterID, explicit []Link) ([]Link, []Diagnostic, error) {
	known := map[string]bool{}
	ports := map[string]Port{}
	for _, c := range components {
		known[c.Name] = true
		for name, p := range c.Ports {
			ports[c.Name+"."+name] = p
		}
	}
	for _, name := range sortedKeys(cluster) {
		if !known[name] {
			return nil, nil, fmt.Errorf("cluster assignment names unknown component %s", name)
		}
	}
	clusterOf := func(port string) ir.ClusterID { return cluster[strings.SplitN(port, ".", 2)[0]] }
	bound := map[string]bool{}
	for i, l := range explicit {
		from, okFrom := ports[l.From]
		_, okTo := ports[l.To]
		if !okFrom || !okTo {
			return nil, nil, fmt.Errorf("links[%d] %s -> %s: unknown port", i, l.From, l.To)
		}
		if strings.HasPrefix(from.Contract, ResourceContractPrefix) && clusterOf(l.From) != clusterOf(l.To) {
			return nil, nil, fmt.Errorf("links[%d] %s -> %s: resource binding crosses clusters %q and %q", i, l.From, l.To, clusterOf(l.From), clusterOf(l.To))
		}
		bound[l.To] = true
	}
	providers := map[string][]string{}
	for _, name := range sortedKeys(ports) {
		if p := ports[name]; p.Direction == "out" && strings.HasPrefix(p.Contract, ResourceContractPrefix) {
			key := string(clusterOf(name)) + "\x00" + p.Contract + "/" + p.Version
			providers[key] = append(providers[key], name)
		}
	}
	links := append([]Link(nil), explicit...)
	var diags []Diagnostic
	for _, name := range sortedKeys(ports) {
		p := ports[name]
		if p.Direction != "in" || !strings.HasPrefix(p.Contract, ResourceContractPrefix) || bound[name] {
			continue
		}
		where := ""
		if c := clusterOf(name); c != "" {
			where = fmt.Sprintf(" in cluster %q", c)
		}
		candidates := providers[string(clusterOf(name))+"\x00"+p.Contract+"/"+p.Version]
		switch len(candidates) {
		case 0:
			diags = append(diags, Diagnostic{Code: DiagUnresolvedProvider, Port: name, Message: fmt.Sprintf("no component writes %s/%s%s", p.Contract, p.Version, where)})
		case 1:
			links = append(links, Link{From: candidates[0], To: name})
		default:
			diags = append(diags, Diagnostic{Code: DiagAmbiguousProvider, Port: name, Message: fmt.Sprintf("%d components write %s/%s%s; add one explicit link to choose the provider", len(candidates), p.Contract, p.Version, where), Candidates: append([]string(nil), candidates...)})
		}
	}
	sort.SliceStable(diags, func(i, j int) bool { return diags[i].Port < diags[j].Port })
	return links, diags, nil
}
