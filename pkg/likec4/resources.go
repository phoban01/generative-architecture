package likec4

import (
	"fmt"
	"sort"
	"strings"

	"generative-architecture/pkg/ir"
)

// readOperations are the API operations drawn as reads; every other API
// operation is a write.
var readOperations = map[string]bool{ir.OpGet: true, ir.OpList: true}

// ResourceView draws what the controllers of an IR document watch, read and
// write: one controller element per controller, one resource element per
// GVK, a "watches" relation from each watched resource to its controller
// (with the mapping and predicates) and a "reads" or "writes" relation from
// each controller to each resource it operates on (with the operation and
// consistency). A schema-only effect reads no object at run time and is a
// "uses schema of" relation. An external effect is a "calls" relation to a boundary
// element for its target. Watches and effects must name a declared
// controller.
func ResourceView(name string, d ir.Document) ([]Element, []Relation, View, error) {
	controllers := map[string]string{}
	elements := map[string]Element{}
	for _, c := range d.Controllers {
		id := controllerID(c.ID)
		controllers[c.ID] = id
		source := c.Provenance.Source
		if c.Setup != nil {
			source = *c.Setup
		}
		title := c.Name
		if title == "" {
			title = c.ID
		}
		elements[id] = Element{ID: id, Kind: KindController, Title: title, Description: "controller " + c.ID, Evidence: ir.Extracted, Source: source}
	}
	actor := func(id, what string) (string, error) {
		e, ok := controllers[id]
		if !ok {
			return "", fmt.Errorf("%s names undeclared controller %q", what, id)
		}
		return e, nil
	}
	// A resource is known as well as the strongest fact that names it.
	resource := func(g ir.GVK, evidence ir.Evidence) string {
		id := ID("resource_" + g.String())
		e, ok := elements[id]
		if !ok {
			e = Element{ID: id, Kind: KindResource, Title: g.String(), Description: "kind " + g.Kind, Evidence: evidence}
		}
		e.Evidence = ir.Strongest(e.Evidence, evidence)
		elements[id] = e
		return id
	}
	var relations []Relation
	for _, w := range d.Watches {
		c, err := actor(w.Controller, "watch "+w.ID)
		if err != nil {
			return nil, nil, View{}, err
		}
		r := resource(w.Resource, w.Evidence)
		relations = append(relations, Relation{From: r, To: c, Label: "watches", Description: watchDescription(w), Evidence: w.Evidence, Source: w.Provenance.Source})
	}
	for _, e := range d.Effects {
		c, err := actor(e.Actor, "effect "+e.ID)
		if err != nil {
			return nil, nil, View{}, err
		}
		if e.External() {
			if e.Target == nil {
				return nil, nil, View{}, fmt.Errorf("external effect %s has no target", e.ID)
			}
			b := ID("external_" + e.Target.String())
			elements[b] = Element{ID: b, Kind: KindBoundary, Title: e.Target.String(), Description: "external call target", Evidence: e.Evidence}
			relations = append(relations, Relation{From: c, To: b, Label: "calls", Description: e.ID + ": " + outcomes(e), Evidence: e.Evidence, Source: e.Provenance.Source})
			continue
		}
		label := "writes"
		switch {
		case e.Consistency == ir.ConsistencySchema:
			label = "uses schema of"
		case readOperations[e.Operation]:
			label = "reads"
		}
		operation := e.Operation
		if e.Subresource != "" {
			operation = e.Subresource + "." + operation
		}
		relations = append(relations, Relation{From: c, To: resource(e.Resource, e.Evidence), Label: label, Description: fmt.Sprintf("%s: %s (%s)", e.ID, operation, e.Consistency), Evidence: e.Evidence, Source: e.Provenance.Source})
	}
	view := View{Name: ID(name), Title: "Resources and watches: " + name}
	var out []Element
	for _, id := range sortedKeys(elements) {
		out = append(out, elements[id])
		view.Include = append(view.Include, id)
	}
	return out, relations, view, nil
}

func controllerID(id string) string { return ID("controller_" + id) }

// watchDescription names the mapping kind and its argument, the predicates
// and whether the watch is the controller's primary resource.
func watchDescription(w ir.Watch) string {
	m := "mapping " + w.Mapping.Kind
	switch {
	case len(w.Mapping.FieldPath) > 0:
		m += " " + strings.Join(w.Mapping.FieldPath, ".")
	case w.Mapping.OwnerGVK != nil:
		m += " " + w.Mapping.OwnerGVK.String()
	}
	parts := []string{m}
	if w.Primary {
		parts = append(parts, "primary")
	}
	var predicates []string
	for _, p := range w.Predicates {
		var args []string
		for _, k := range sortedKeys(p.Args) {
			args = append(args, k+"="+p.Args[k])
		}
		if len(args) > 0 {
			predicates = append(predicates, p.Kind+"("+strings.Join(args, ", ")+")")
		} else {
			predicates = append(predicates, p.Kind)
		}
	}
	sort.Strings(predicates)
	if len(predicates) > 0 {
		parts = append(parts, "predicates "+strings.Join(predicates, ", "))
	}
	return strings.Join(parts, "; ")
}

func outcomes(e ir.Effect) string {
	var names []string
	for _, o := range e.Outcomes {
		names = append(names, o.Name)
	}
	return "outcomes " + strings.Join(names, ", ")
}
