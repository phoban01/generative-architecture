package adaptergen

import (
	"fmt"

	"generative-architecture/pkg/ir"
)

// kindConstants names the ir constant for each node kind in generated code.
var kindConstants = map[string]string{
	ir.NodeBranch: "ir.NodeBranch",
	ir.NodeEffect: "ir.NodeEffect",
	ir.NodeAssign: "ir.NodeAssign",
	ir.NodeReturn: "ir.NodeReturn",
}

type gatedNode struct {
	ID     string
	Kind   string
	Const  string
	Effect string
}

func gatedNodes(p ir.Process) ([]gatedNode, error) {
	if p.ID == "" {
		return nil, fmt.Errorf("process has no id")
	}
	if len(p.Nodes) == 0 {
		return nil, fmt.Errorf("process %s has no nodes", p.ID)
	}
	ids := map[string]bool{}
	for _, n := range p.Nodes {
		if n.ID == "" {
			return nil, fmt.Errorf("process %s: node has no id", p.ID)
		}
		if ids[n.ID] {
			return nil, fmt.Errorf("process %s: duplicate node %s", p.ID, n.ID)
		}
		ids[n.ID] = true
	}
	if p.Entry != "" && !ids[p.Entry] {
		return nil, fmt.Errorf("process %s: entry %s is not a node", p.ID, p.Entry)
	}
	nodes := make([]gatedNode, 0, len(p.Nodes))
	for _, n := range p.Nodes {
		c, ok := kindConstants[n.Kind]
		if !ok {
			return nil, fmt.Errorf("process %s: node %s has unknown kind %q; expected branch, effect, assign or return", p.ID, n.ID, n.Kind)
		}
		if n.Kind == ir.NodeEffect && n.Effect == "" {
			return nil, fmt.Errorf("process %s: effect node %s names no effect", p.ID, n.ID)
		}
		if n.Kind != ir.NodeEffect && n.Effect != "" {
			return nil, fmt.Errorf("process %s: %s node %s names effect %s", p.ID, n.Kind, n.ID, n.Effect)
		}
		for _, next := range []string{n.Next, n.Else} {
			if next != "" && !ids[next] {
				return nil, fmt.Errorf("process %s: node %s continues to unknown node %s", p.ID, n.ID, next)
			}
		}
		nodes = append(nodes, gatedNode{ID: n.ID, Kind: n.Kind, Const: c, Effect: n.Effect})
	}
	return nodes, nil
}

// GenerateGating emits a file of package pkg defining
//
//	func ApplyProcessEvent(ctx context.Context, gate *effect.Gate, nodes map[string]ir.Node, event string, observe func(id string, r effect.Result)) error
//
// for the process p. The event is a node ID. An effect node waits on the
// gate for the controller's next boundary, requires its descriptor to be the
// node's effect, completes the call and reports the result to observe.
// Branch and assign nodes are no-ops; return nodes are left to the caller.
// The nodes map is the loaded process: each case checks the node it was
// generated for still has that kind and effect, so stale generated code
// fails instead of gating the wrong boundary.
func GenerateGating(pkg string, p ir.Process) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	nodes, err := gatedNodes(p)
	if err != nil {
		return nil, err
	}
	return render("gating", gatingTemplate, struct {
		Header  string
		Package string
		ID      string
		Nodes   []gatedNode
	}{Header, pkg, p.ID, nodes})
}

const gatingTemplate = `{{.Header}}

package {{.Package}}

import (
	"context"
	"fmt"

	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/ir"
)

// ApplyProcessEvent gates the node of process {{.ID}} named event at its
// effect boundary. An effect node waits for the controller to reach the gate,
// checks it reached the node's effect, completes the call and passes the
// result to observe. Branch and assign nodes make no API call and are no-ops;
// return nodes are left to the caller, which owns the invocation. The loaded
// nodes must agree with the process this file was generated from.
func ApplyProcessEvent(ctx context.Context, gate *effect.Gate, nodes map[string]ir.Node, event string, observe func(id string, r effect.Result)) error {
	node, ok := nodes[event]
	if !ok {
		return fmt.Errorf("unknown process event %q", event)
	}
	switch event {
{{- range .Nodes}}
	case {{q .ID}}:
{{- if eq .Kind "effect"}}
		if err := gatingExpect(event, node, ir.NodeEffect, {{q .Effect}}); err != nil {
			return err
		}
		return gatingBoundary(ctx, gate, event, {{q .Effect}}, observe)
{{- else}}
		return gatingExpect(event, node, {{.Const}}, "")
{{- end}}
{{- end}}
	}
	return fmt.Errorf("process event %q is not a node of process %s", event, {{q .ID}})
}

// gatingExpect checks that a loaded node still has the kind and effect this
// file was generated for.
func gatingExpect(id string, node ir.Node, kind, effectID string) error {
	if node.Kind != kind || node.Effect != effectID {
		return fmt.Errorf("node %s: generated for %s %q, loaded as %s %q", id, kind, effectID, node.Kind, node.Effect)
	}
	return nil
}

// gatingBoundary waits for the controller's next effect boundary, requires it
// to be effectID, completes it without injecting an error and reports the
// result. A mismatched boundary is left pending for the caller to release by
// cancelling the invocation context.
func gatingBoundary(ctx context.Context, gate *effect.Gate, id, effectID string, observe func(string, effect.Result)) error {
	boundary, err := gate.Next(ctx)
	if err != nil {
		return err
	}
	if boundary.Call.Descriptor.ID != effectID {
		return fmt.Errorf("node %s: expected effect %s, got %s", id, effectID, boundary.Call.Descriptor.ID)
	}
	result, err := boundary.Complete(ctx, nil)
	if err != nil {
		return err
	}
	if observe != nil {
		observe(id, result)
	}
	return result.Err
}
`
