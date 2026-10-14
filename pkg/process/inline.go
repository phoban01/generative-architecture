package process

import (
	"fmt"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
)

// Call binds an extracted subreconciler's symbols and return values to its
// caller's frame. Bindings are explicit; child control flow is source-derived.
type Call struct {
	Program  goflow.Program
	Bindings map[string]expr.Expression
	Results  []string
}

// Inline resolves a finite, acyclic call graph. Each call receives its own nodes;
// bindings determine which state is shared and which locals are private. The
// returned source hashes identify the parent; retain each child Program's hashes
// in the caller's provenance receipt as well.
func Inline(parent goflow.Program, calls map[string]Call) (goflow.Program, error) {
	return inline(parent, calls, map[string]bool{})
}
func inline(parent goflow.Program, calls map[string]Call, stack map[string]bool) (goflow.Program, error) {
	out := goflow.Program{SourceSHA256: parent.SourceSHA256, MethodSHA256: parent.MethodSHA256}
	entries := map[int]int{}
	type edge struct {
		index, next, other int
		branch             bool
	}
	var edges []edge
	for _, original := range parent.Nodes {
		if original.ID < 0 {
			return out, fmt.Errorf("negative process node")
		}
		if _, ok := entries[original.ID]; ok {
			return out, fmt.Errorf("duplicate process node")
		}
		entries[original.ID] = len(out.Nodes)
		if original.Kind != "call" {
			n := original
			n.ID = len(out.Nodes)
			if n.Kind != "return" {
				edges = append(edges, edge{n.ID, n.Next, n.Else, n.Kind == "branch"})
			}
			out.Nodes = append(out.Nodes, n)
			continue
		}
		call, ok := calls[original.Callee]
		if !ok {
			return out, fmt.Errorf("unsummarized subreconciler %s", original.Callee)
		}
		if stack[original.Callee] {
			return out, fmt.Errorf("recursive process calls unsupported")
		}
		stack[original.Callee] = true
		child, err := inline(call.Program, calls, stack)
		delete(stack, original.Callee)
		if err != nil {
			return out, err
		}
		offset := len(out.Nodes)
		entries[original.ID] = offset + child.Entry
		for _, n := range child.Nodes {
			n.ID += offset
			if n.Kind == "return" {
				if len(n.Results) != len(call.Results) {
					return out, fmt.Errorf("subreconciler result arity mismatch")
				}
				n.Kind = "assign"
				n.Updates = map[string]expr.Expression{}
				for i, target := range call.Results {
					if target == "" {
						continue
					}
					if _, exists := n.Updates[target]; exists {
						return out, fmt.Errorf("duplicate return target")
					}
					value, err := expr.Rewrite(n.Results[i], call.Bindings)
					if err != nil {
						return out, err
					}
					n.Updates[target] = value
				}
				n.Results = nil
				n.Expression = ""
				n.ReturnError = ""
				edges = append(edges, edge{index: n.ID, next: original.Next})
			} else {
				n.Next += offset
				if n.Kind == "branch" {
					n.Else += offset
				}
				if n.Expression != "" {
					n.Expression, err = expr.Rewrite(n.Expression, call.Bindings)
					if err != nil {
						return out, err
					}
				}
				updates := map[string]expr.Expression{}
				for name, value := range n.Updates {
					target := name
					if mapped, ok := call.Bindings[name]; ok {
						target = string(mapped)
					}
					if _, exists := updates[target]; exists {
						return out, fmt.Errorf("aliased subreconciler writes")
					}
					updates[target], err = expr.Rewrite(value, call.Bindings)
					if err != nil {
						return out, err
					}
				}
				n.Updates = updates
				if len(n.Bindings) > 0 {
					bindings := make(map[string]string, len(n.Bindings))
					for placeholder, value := range n.Bindings {
						bound, err := expr.Rewrite(expr.Expression(value), call.Bindings)
						if err != nil {
							return out, err
						}
						bindings[placeholder] = string(bound)
					}
					n.Bindings = bindings
				}
			}
			out.Nodes = append(out.Nodes, n)
		}
	}
	for _, e := range edges {
		next, ok := entries[e.next]
		if !ok {
			return out, fmt.Errorf("invalid successor %d", e.next)
		}
		out.Nodes[e.index].Next = next
		if e.branch {
			other, ok := entries[e.other]
			if !ok {
				return out, fmt.Errorf("invalid branch successor")
			}
			out.Nodes[e.index].Else = other
		}
	}
	entry, ok := entries[parent.Entry]
	if !ok {
		return out, fmt.Errorf("missing process entry")
	}
	out.Entry = entry
	return out, nil
}
