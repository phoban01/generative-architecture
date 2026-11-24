package likec4

import (
	"fmt"

	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/sourcegen"
)

// StructureView draws the Go types and declared controller mappings of a
// source descriptor: one resource element per API type, one controller
// element per declared controller, and one relation per declared port, from
// the controller to the resource for an out port and the other way for an
// in port. Everything in the view is declared, not extracted: the markers
// name the mappings, and runtime registration is not inferred.
func StructureView(name string, d sourcegen.Descriptor) ([]Element, []Relation, View, error) {
	view := View{Name: ID(name), Title: "Go types and declared controller mappings", Layout: "TopBottom"}
	resources := map[string]string{}
	var elements []Element
	for _, r := range d.Resources {
		if _, dup := resources[r.ID]; dup {
			return nil, nil, View{}, fmt.Errorf("resource %s listed twice", r.ID)
		}
		id := ID("resource_" + r.ID)
		resources[r.ID] = id
		elements = append(elements, Element{
			ID: id, Kind: KindResource, Title: r.Kind,
			Description: r.Group + "/" + r.Version + "; " + r.ID,
			Evidence:    ir.Declared,
			Source:      ir.Source{File: r.Source.File, Line: r.Source.Line},
		})
		view.Include = append(view.Include, id)
	}
	var relations []Relation
	for _, c := range d.Controllers {
		id := ID("controller_" + c.Component)
		elements = append(elements, Element{
			ID: id, Kind: KindController, Title: c.Component + " controller",
			Description: c.GoType + "; declared primary " + c.Primary + "; declared owns " + c.Owns + "; runtime registration not inferred",
			Evidence:    ir.Declared,
			Source:      ir.Source{File: c.Source.File, Line: c.Source.Line},
		})
		view.Include = append(view.Include, id)
		for _, p := range c.Ports {
			resource, ok := resources[p.Resource]
			if !ok {
				return nil, nil, View{}, fmt.Errorf("controller %s port %s names unknown resource %s", c.Component, p.Name, p.Resource)
			}
			from, to := id, resource
			if p.Direction == "in" {
				from, to = to, from
			}
			relations = append(relations, Relation{
				From: from, To: to,
				Label:       p.Name + " / " + p.JSONPath,
				Description: p.Type + "; " + p.Protocol + "; " + p.Evidence,
				Evidence:    ir.Declared,
				Source:      ir.Source{File: c.Source.File, Line: c.Source.Line},
			})
		}
	}
	return elements, relations, view, nil
}
