package likec4

import (
	"fmt"

	"generative-architecture/pkg/ir"
)

// ProcessView draws the control flow of one IR process: a step element per
// node, titled with its Go source when the frontend kept it, described by
// its kind (and effect) and linked to its file:line. A branch has a "true"
// relation to Next and a "false" relation to Else; any other node except a
// return has a "next" relation. The process is extracted from source, so
// every element and relation is extracted evidence.
func ProcessView(name, title string, p ir.Process) ([]Element, []Relation, View, error) {
	ids := map[string]string{}
	for _, n := range p.Nodes {
		if _, dup := ids[n.ID]; dup {
			return nil, nil, View{}, fmt.Errorf("process %s: node %s listed twice", p.ID, n.ID)
		}
		ids[n.ID] = ID(p.ID + "_" + n.ID)
	}
	if _, ok := ids[p.Entry]; !ok {
		return nil, nil, View{}, fmt.Errorf("process %s: entry %s is not a node", p.ID, p.Entry)
	}
	view := View{Name: ID(name), Title: title}
	var elements []Element
	var relations []Relation
	edge := func(n ir.Node, to, label string) error {
		target, ok := ids[to]
		if !ok {
			return fmt.Errorf("process %s: node %s goes to unknown node %q", p.ID, n.ID, to)
		}
		relations = append(relations, Relation{From: ids[n.ID], To: target, Label: label, Evidence: ir.Extracted, Source: n.Source})
		return nil
	}
	for _, n := range p.Nodes {
		description := n.Kind
		if n.Effect != "" {
			description += " " + n.Effect
		}
		if n.ID == p.Entry {
			description += "; entry"
		}
		elements = append(elements, Element{ID: ids[n.ID], Kind: KindStep, Title: stepTitle(n), Description: description, Evidence: ir.Extracted, Source: n.Source})
		view.Include = append(view.Include, ids[n.ID])
		var err error
		switch n.Kind {
		case ir.NodeReturn:
		case ir.NodeBranch:
			if err = edge(n, n.Next, "true"); err == nil {
				err = edge(n, n.Else, "false")
			}
		default:
			err = edge(n, n.Next, "next")
		}
		if err != nil {
			return nil, nil, View{}, err
		}
	}
	return elements, relations, view, nil
}

// stepTitle is the node's Go source, or else what the IR says it does.
func stepTitle(n ir.Node) string {
	switch {
	case n.Code != "":
		return n.Code
	case n.Kind == ir.NodeBranch || n.Kind == ir.NodeAssign:
		return n.Expression
	case n.Effect != "":
		return n.Effect
	}
	return n.Kind
}
