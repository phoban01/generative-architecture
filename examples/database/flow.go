package database

import (
	"fmt"
	"strconv"

	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/likec4"
)

// Process converts the extracted Reconcile into an IR process. Store
// operations become effect nodes named by their effect; node n has the IR
// id "step<n>", and each node keeps its Go source and line.
func (e Example) Process() (ir.Process, error) {
	return e.process(func(n int) string { return "step" + strconv.Itoa(n) })
}

// eventProcess is Process with each node named by the model's process
// event, node<n>_<kind>, as the generated gating addresses it.
func (e Example) eventProcess() (ir.Process, error) {
	return e.process(func(n int) string {
		if n < 0 || n >= len(e.Program.Nodes) {
			return ""
		}
		return fmt.Sprintf("node%d_%s", n, e.Program.Nodes[n].Kind)
	})
}

func (e Example) process(id func(n int) string) (ir.Process, error) {
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

// controllerID names the Database controller in the IR the diagrams draw.
const controllerID = "database"

// Workspace draws the example with pkg/likec4 in one workspace with one
// specification. The resource view shows the watches extracted from
// wiring.go, the dynamic-client effects extracted from dynamic_store.go
// and the derived effects the model declares in generate.go. The process
// view shows the extracted control-flow graph of Reconcile.
func (e Example) Workspace() (likec4.Workspace, error) {
	var w likec4.Workspace
	doc := ir.Document{
		Controllers: []ir.Controller{{ID: controllerID, Name: "DatabaseReconciler", Provenance: ir.Provenance{Source: ir.Source{File: "controller.go"}}}},
		Watches:     e.Watches.IR(controllerID, "wiring.go"),
		Effects:     e.Dynamic.IR(controllerID, "dynamic_store.go"),
	}
	extracted := map[string]bool{}
	for _, eff := range doc.Effects {
		extracted[eff.ID] = true
	}
	var declared []effect.Descriptor
	for _, d := range e.Spec.Effects {
		if !extracted[d.ID] {
			declared = append(declared, d)
		}
	}
	doc.Effects = append(doc.Effects, likec4.EffectsOf(controllerID, declared, ir.Declared, ir.Source{File: "generate.go"})...)
	elements, relations, view, err := likec4.ResourceView("database_resources", doc)
	if err != nil {
		return w, err
	}
	view.Title = "Database controller: watches and API effects"
	if err := w.Add(elements, relations, view); err != nil {
		return w, err
	}
	p, err := e.Process()
	if err != nil {
		return w, err
	}
	if elements, relations, view, err = likec4.ProcessView("database_provisioning_flow", "Database controller: extracted reconcile flow", p); err != nil {
		return w, err
	}
	if err := w.Add(elements, relations, view); err != nil {
		return w, err
	}
	return w, w.Validate()
}

// LikeC4 renders Workspace as one .c4 file.
func (e Example) LikeC4() (string, error) {
	w, err := e.Workspace()
	if err != nil {
		return "", err
	}
	src, err := w.Source()
	return string(src), err
}
