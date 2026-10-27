package likec4

import (
	"fmt"
	"sort"
	"strings"

	"generative-architecture/pkg/composition"
	"generative-architecture/pkg/ir"
)

// ContextView draws a composition as a system context: one element per
// component and one relation per link, labelled <contract>/<version>.
//
// evidence says how each component is known, by component name. A component
// whose model was extracted or observed is a controller; one that is only
// declared, assumed or unresolved is a boundary. A relation is as strong as
// its weaker end. sources optionally locates each component; a relation
// takes the source of the component it starts from.
//
// It takes the components rather than composition.Result because the
// result keeps no ports, and the ports carry the contracts.
func ContextView(name string, components []composition.Component, evidence map[string]ir.Evidence, links []composition.Link, sources map[string]ir.Source) ([]Element, []Relation, View, error) {
	byName := map[string]composition.Component{}
	var elements []Element
	view := View{Name: ID(name), Title: "System context: " + name}
	for _, c := range components {
		if _, dup := byName[c.Name]; dup {
			return nil, nil, View{}, fmt.Errorf("component %s listed twice", c.Name)
		}
		byName[c.Name] = c
		e, ok := evidence[c.Name]
		if !ok {
			return nil, nil, View{}, fmt.Errorf("component %s has no evidence", c.Name)
		}
		kind := KindBoundary
		if e == ir.Extracted || e == ir.Observed {
			kind = KindController
		}
		id := ID("component_" + c.Name)
		elements = append(elements, Element{ID: id, Kind: kind, Title: c.Name, Description: ports(c), Evidence: e, Source: sources[c.Name]})
		view.Include = append(view.Include, id)
	}
	var relations []Relation
	for _, l := range links {
		from, fromPort, err := endpoint(byName, l.From)
		if err != nil {
			return nil, nil, View{}, err
		}
		to, toPort, err := endpoint(byName, l.To)
		if err != nil {
			return nil, nil, View{}, err
		}
		if fromPort.Contract != toPort.Contract || fromPort.Version != toPort.Version {
			return nil, nil, View{}, fmt.Errorf("link %s -> %s joins %s/%s to %s/%s", l.From, l.To, fromPort.Contract, fromPort.Version, toPort.Contract, toPort.Version)
		}
		relations = append(relations, Relation{
			From: ID("component_" + from), To: ID("component_" + to),
			Label:       fromPort.Contract + "/" + fromPort.Version,
			Description: "link " + l.From + " -> " + l.To,
			Evidence:    weaker(evidence[from], evidence[to]),
			Source:      sources[from],
		})
	}
	return elements, relations, view, nil
}

// endpoint resolves component.port.
func endpoint(components map[string]composition.Component, ref string) (string, composition.Port, error) {
	name, port, ok := strings.Cut(ref, ".")
	if !ok {
		return "", composition.Port{}, fmt.Errorf("link end %q is not component.port", ref)
	}
	c, ok := components[name]
	if !ok {
		return "", composition.Port{}, fmt.Errorf("link end %q names an unknown component", ref)
	}
	p, ok := c.Ports[port]
	if !ok {
		return "", composition.Port{}, fmt.Errorf("link end %q names an unknown port", ref)
	}
	return name, p, nil
}

// ports describes a component's ports in name order.
func ports(c composition.Component) string {
	names := make([]string, 0, len(c.Ports))
	for n := range c.Ports {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		p := c.Ports[n]
		out = append(out, fmt.Sprintf("%s %s %s/%s", n, p.Direction, p.Contract, p.Version))
	}
	if len(out) == 0 {
		return "no ports"
	}
	return "ports: " + strings.Join(out, ", ")
}

// weaker returns the lower-ranked of two evidence kinds.
func weaker(a, b ir.Evidence) ir.Evidence {
	if ir.Strongest(a, b) == a {
		return b
	}
	return a
}
