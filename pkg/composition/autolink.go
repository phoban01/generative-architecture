package composition

import (
	"fmt"
	"sort"
)

// AutoLink connects each input to the unique compatible output contract.
// Existing explicit links disambiguate deployments with multiple providers.
// Matching a contract does not infer ownership or physical network topology.
func AutoLink(components []Component, explicit []Link) ([]Link, error) {
	links := append([]Link(nil), explicit...)
	bound := map[string]bool{}
	for _, link := range explicit {
		bound[link.To] = true
	}
	providers := map[string][]string{}
	type input struct{ name, contract string }
	var inputs []input
	for _, c := range components {
		for name, p := range c.Ports {
			ref := p.Contract + "/" + p.Version
			if p.Direction == "out" {
				providers[ref] = append(providers[ref], c.Name+"."+name)
			}
			if p.Direction == "in" {
				inputs = append(inputs, input{c.Name + "." + name, ref})
			}
		}
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].name < inputs[j].name })
	for _, p := range inputs {
		if bound[p.name] {
			continue
		}
		candidates := providers[p.contract]
		sort.Strings(candidates)
		if len(candidates) != 1 {
			return nil, fmt.Errorf("input %s requires one provider of %s; found %v; supply an explicit binding", p.name, p.contract, candidates)
		}
		links = append(links, Link{From: candidates[0], To: p.name})
	}
	return links, nil
}
