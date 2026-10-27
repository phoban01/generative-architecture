package adaptergen

import (
	"fmt"

	"github.com/phoban01/generative-architecture/pkg/ir"
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

	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/ir"
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

// BoundaryEvent names one outcome of an effect of a process: replaying the
// event completes the controller's pending boundary for Effect, injecting a
// transient error instead of executing the call when Error is set.
type BoundaryEvent struct {
	Name   string
	Effect string
	Error  bool
}

type boundaryCase struct {
	Name      string
	Effect    string
	Step      int
	Error     bool
	Pinned    bool
	Namespace string
	Object    string
}

// effectSteps numbers the effects of p in breadth-first order from the
// entry, following Next before Else: the position of each effect along the
// process. An effect performed by two nodes has no single position and is
// rejected.
func effectSteps(p ir.Process) (map[string]int, error) {
	nodes := map[string]ir.Node{}
	for _, n := range p.Nodes {
		nodes[n.ID] = n
	}
	entry := p.Entry
	if entry == "" {
		entry = p.Nodes[0].ID
	}
	steps := map[string]int{}
	performed := map[string]string{}
	for _, n := range p.Nodes {
		if n.Kind != ir.NodeEffect {
			continue
		}
		if other, ok := performed[n.Effect]; ok {
			return nil, fmt.Errorf("process %s: effect %s is performed by nodes %s and %s", p.ID, n.Effect, other, n.ID)
		}
		performed[n.Effect] = n.ID
	}
	seen := map[string]bool{entry: true}
	queue := []string{entry}
	for len(queue) > 0 {
		n := nodes[queue[0]]
		queue = queue[1:]
		if n.Kind == ir.NodeEffect {
			steps[n.Effect] = len(steps) + 1
		}
		for _, next := range []string{n.Next, n.Else} {
			if next != "" && !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	for effectID, id := range performed {
		if _, ok := steps[effectID]; !ok {
			return nil, fmt.Errorf("process %s: effect node %s is unreachable from the entry", p.ID, id)
		}
	}
	return steps, nil
}

func boundaryCases(p ir.Process, events []BoundaryEvent, targets map[string]ir.ObjectRef) ([]boundaryCase, int, error) {
	if _, err := gatedNodes(p); err != nil {
		return nil, 0, err
	}
	steps, err := effectSteps(p)
	if err != nil {
		return nil, 0, err
	}
	if len(events) == 0 {
		return nil, 0, fmt.Errorf("process %s: at least one boundary event is required", p.ID)
	}
	for effectID, key := range targets {
		if _, ok := steps[effectID]; !ok {
			return nil, 0, fmt.Errorf("process %s: target given for %s, which is not an effect of the process", p.ID, effectID)
		}
		if key.Name == "" || key.Cluster != "" {
			return nil, 0, fmt.Errorf("process %s: target of %s must name one object of the implicit cluster", p.ID, effectID)
		}
	}
	names := map[string]bool{}
	outcomes := map[string]bool{}
	cases := make([]boundaryCase, 0, len(events))
	for _, e := range events {
		if e.Name == "" {
			return nil, 0, fmt.Errorf("process %s: boundary event of %s has no name", p.ID, e.Effect)
		}
		if names[e.Name] {
			return nil, 0, fmt.Errorf("process %s: boundary event %s declared twice", p.ID, e.Name)
		}
		names[e.Name] = true
		step, ok := steps[e.Effect]
		if !ok {
			return nil, 0, fmt.Errorf("process %s: boundary event %s names %q, which is not an effect of the process", p.ID, e.Name, e.Effect)
		}
		outcome := fmt.Sprintf("%s/%t", e.Effect, e.Error)
		if outcomes[outcome] {
			return nil, 0, fmt.Errorf("process %s: two boundary events complete %s with error=%t", p.ID, e.Effect, e.Error)
		}
		outcomes[outcome] = true
		key, pinned := targets[e.Effect]
		cases = append(cases, boundaryCase{Name: e.Name, Effect: e.Effect, Step: step, Error: e.Error, Pinned: pinned, Namespace: key.Namespace, Object: key.Name})
	}
	for effectID := range steps {
		if !outcomes[effectID+"/false"] {
			return nil, 0, fmt.Errorf("process %s: no boundary event completes %s successfully", p.ID, effectID)
		}
	}
	return cases, len(steps), nil
}

// GenerateBoundaryGating emits a file of package pkg defining
//
//	func ApplyBoundary(ctx context.Context, gate *effect.Gate, event string) (Boundary, bool, error)
//
// for the process p. Each event completes the controller's next effect
// boundary, which must be the event's effect and, when targets names an
// object for that effect, address that object. A success event executes the
// call and requires it to succeed; an error event injects a transient error.
// The returned Boundary carries the completed result and the effect's
// position along the process (1 for the first effect reached from the entry;
// BoundarySteps is the number of effects), so the caller can follow the
// process without a copy of it. The bool reports whether event named a
// boundary, so callers can route other events elsewhere. Every effect of p
// needs a success event; error events are optional.
func GenerateBoundaryGating(pkg string, p ir.Process, events []BoundaryEvent, targets map[string]ir.ObjectRef) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	cases, steps, err := boundaryCases(p, events, targets)
	if err != nil {
		return nil, err
	}
	return render("boundary gating", boundaryGatingTemplate, struct {
		Header  string
		Package string
		ID      string
		Steps   int
		Cases   []boundaryCase
	}{Header, pkg, p.ID, steps, cases})
}

const boundaryGatingTemplate = `{{.Header}}

package {{.Package}}

import (
	"context"
	"errors"
	"fmt"

	"github.com/phoban01/generative-architecture/pkg/effect"
)

// BoundarySteps is the number of effects of process {{.ID}}.
const BoundarySteps = {{.Steps}}

// Boundary is one completed effect boundary of process {{.ID}}.
type Boundary struct {
	// Effect is the effect the controller reached.
	Effect string
	// Step is the effect's position along the process, from 1.
	Step int64
	// Result is the completed call; Result.Err is set exactly when the
	// event injected an error.
	Result effect.Result
}

// ApplyBoundary completes the effect boundary of process {{.ID}} that event
// names. It returns false when event names no boundary.
func ApplyBoundary(ctx context.Context, gate *effect.Gate, event string) (Boundary, bool, error) {
	switch event {
{{- range .Cases}}
	case {{q .Name}}:
		b, err := gatingComplete(ctx, gate, {{q .Effect}}, {{.Step}}, {{if .Pinned}}&effect.Key{Namespace: {{q .Namespace}}, Name: {{q .Object}}}{{else}}nil{{end}}, {{.Error}})
		return b, true, err
{{- end}}
	}
	return Boundary{}, false, nil
}

// gatingInjected is the transient error an error event injects.
var gatingInjected = errors.New("injected transient error")

// gatingComplete waits for the controller's next effect boundary, requires
// it to be effectID on the expected object, completes it (injecting an error
// when fail is set) and checks the outcome. A mismatched boundary is left
// pending for the caller to release by cancelling the invocation context.
func gatingComplete(ctx context.Context, gate *effect.Gate, effectID string, step int64, key *effect.Key, fail bool) (Boundary, error) {
	b, err := gate.Next(ctx)
	if err != nil {
		return Boundary{}, err
	}
	if b.Call.Descriptor.ID != effectID {
		return Boundary{}, fmt.Errorf("expected effect %s, got %s", effectID, b.Call.Descriptor.ID)
	}
	if key != nil && b.Call.Key != *key {
		return Boundary{}, fmt.Errorf("effect %s: unexpected object key %+v", effectID, b.Call.Key)
	}
	var injected error
	if fail {
		injected = gatingInjected
	}
	result, err := b.Complete(ctx, injected)
	if err != nil {
		return Boundary{}, err
	}
	if (result.Err != nil) != fail {
		return Boundary{}, fmt.Errorf("effect %s: unexpected API outcome: %v", effectID, result.Err)
	}
	return Boundary{Effect: effectID, Step: step, Result: result}, nil
}
`
