package database

import (
	"fmt"
	"strconv"

	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/likec4"
)

// Process converts the extracted Reconcile into an IR process. Store
// operations become effect nodes named by their effect; node n has the IR
// id "step<n>", and each node keeps its Go source and line.
func (e Example) Process() (ir.Process, error) {
	id := func(n int) string { return "step" + strconv.Itoa(n) }
	out := ir.Process{ID: "db", Controller: "DatabaseReconciler", Method: "Reconcile", Entry: id(e.Program.Entry)}
	effects := map[string]bool{}
	for _, d := range e.Spec.Effects {
		effects[d.ID] = true
	}
	for _, n := range e.Program.Nodes {
		node := ir.Node{ID: id(n.ID), Kind: n.Kind, Source: ir.Source{File: "controller.go", Line: n.Line}, Code: n.Source}
		switch {
		case n.Kind == ir.NodeReturn:
		case n.Kind == ir.NodeBranch:
			node.Expression, node.Next, node.Else = string(n.Expression), id(n.Next), id(n.Else)
		case effects[n.Kind]:
			node.Kind, node.Effect, node.Next = ir.NodeEffect, n.Kind, id(n.Next)
		default:
			return ir.Process{}, fmt.Errorf("node %d of kind %s has no IR form", n.ID, n.Kind)
		}
		out.Nodes = append(out.Nodes, node)
	}
	return out, nil
}

// FlowLikeC4 draws the extracted control-flow graph with likec4.ProcessView
// rather than a manually maintained copy of the four-step story. Return
// nodes end a reconcile attempt.
func (e Example) FlowLikeC4() (string, error) {
	p, err := e.Process()
	if err != nil {
		return "", err
	}
	elements, relations, view, err := likec4.ProcessView("database_provisioning_flow", "Database controller: extracted reconcile flow", p)
	if err != nil {
		return "", err
	}
	var w likec4.Workspace
	if err := w.Add(elements, relations, view); err != nil {
		return "", err
	}
	src, err := w.Source()
	return string(src), err
}
