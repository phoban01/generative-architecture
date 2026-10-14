package conformance

import (
	"fmt"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/model"
	"strings"
)

// MinimizePrefix returns the shortest prefix of t that still violates a
// property of m. Property violation is monotone in the prefix length, so a
// binary search over the length with m.Check finds the prefix ending at the
// first violating state. As in Replay, any failing check counts as a
// violation. The error is a *Failure of category tool when t violates no
// property. Full delta debugging over the actions is out of scope: the result
// is a prefix, so it is still a valid trace of the model.
func MinimizePrefix(m *model.Compiled, t Trace) (Trace, error) {
	if len(t.States) == 0 {
		return Trace{}, fail(CategoryTool, 0, "", fmt.Errorf("empty trace"))
	}
	// Memoize per-state results: probes overlap and every check is a CEL
	// evaluation.
	checked := make([]bool, len(t.States))
	evaluated := make([]bool, len(t.States))
	violates := func(length int) bool {
		for i := 0; i < length; i++ {
			if !evaluated[i] {
				checked[i] = m.Check(t.States[i].Values, t.States[i].Event) != nil
				evaluated[i] = true
			}
			if checked[i] {
				return true
			}
		}
		return false
	}
	if !violates(len(t.States)) {
		last := len(t.States) - 1
		return Trace{}, fail(CategoryTool, last, t.States[last].Event, fmt.Errorf("trace satisfies every property; nothing to minimize"))
	}
	// Invariant: the prefix of length hi violates and the one of length lo-1
	// does not.
	lo, hi := 1, len(t.States)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if violates(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	out := Trace{Status: t.Status, States: make([]State, hi)}
	copy(out.States, t.States[:hi])
	return out, nil
}

// SourceLinks maps each state of t to the source of the process node whose
// action produced it. Actions generated from a process are named
// node<id>_<operation>; the entry is zero-valued for any other event, such
// as init or environment actions, and for ids absent from p. The result has
// one entry per state.
func SourceLinks(t Trace, p ir.Process) []ir.Source {
	sources := map[string]ir.Source{}
	for _, n := range p.Nodes {
		sources[n.ID] = n.Source
	}
	out := make([]ir.Source, len(t.States))
	for i, s := range t.States {
		if id, ok := NodeID(s.Event); ok {
			out[i] = sources[id]
		}
	}
	return out
}

// NodeID extracts the process node id from a generated action name of the
// form node<id>_<operation>.
func NodeID(event string) (string, bool) {
	rest, ok := strings.CutPrefix(event, "node")
	if !ok {
		return "", false
	}
	id, _, ok := strings.Cut(rest, "_")
	if !ok || id == "" {
		return "", false
	}
	return id, true
}
