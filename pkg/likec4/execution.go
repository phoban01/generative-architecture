package likec4

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/ir"
)

// idleEvent is the stutter action; it changes nothing and is not drawn.
const idleEvent = "idle"

// ExecutionView draws a trace, such as a counterexample, as a dynamic view.
// The initial state and every event except idle become step elements, and
// the view plays one step per event from the previous drawn state. A step's
// notes list the variables the event changed (old -> new) and its source.
//
// links holds the source of each state's event, one entry per state, as
// conformance.SourceLinks returns; nil means no sources. A step with a
// source comes from an extracted process node and carries extracted
// evidence. Any other step is an environment or runtime event the model
// assumes, and carries assumed evidence.
func ExecutionView(name string, t conformance.Trace, links []ir.Source) ([]Element, []Relation, View, error) {
	if len(t.States) == 0 {
		return nil, nil, View{}, fmt.Errorf("trace %s has no states", name)
	}
	if links != nil && len(links) != len(t.States) {
		return nil, nil, View{}, fmt.Errorf("trace %s: %d source links for %d states", name, len(links), len(t.States))
	}
	view := View{Name: ID(name), Title: "Execution: " + name, Dynamic: true}
	start := ID(name + "_s0")
	elements := []Element{{ID: start, Kind: KindStep, Title: "initial state", Description: "state 0", Evidence: ir.Assumed}}
	var relations []Relation
	prev, prevID := t.States[0].Values, start
	for i := 1; i < len(t.States); i++ {
		s := t.States[i]
		if s.Event == idleEvent {
			continue
		}
		var source ir.Source
		if links != nil {
			source = links[i]
		}
		evidence := ir.Assumed
		if source.File != "" {
			evidence = ir.Extracted
		}
		delta := changes(prev, s.Values)
		id := ID(name + "_s" + strconv.Itoa(i))
		elements = append(elements, Element{ID: id, Kind: KindStep, Title: s.Event, Description: fmt.Sprintf("state %d; %s", i, delta), Evidence: evidence, Source: source})
		relations = append(relations, Relation{From: prevID, To: id, Label: s.Event, Evidence: evidence, Source: source})
		notes := delta
		if l := Location(source); l != "" {
			notes += "; source: " + l
		}
		view.Steps = append(view.Steps, Step{From: prevID, To: id, Label: strconv.Itoa(i) + ". " + s.Event, Description: notes})
		prev, prevID = s.Values, id
	}
	if len(view.Steps) == 0 {
		return nil, nil, View{}, fmt.Errorf("trace %s has no event other than idle", name)
	}
	return elements, relations, view, nil
}

// changes lists, in name order, the variables whose value differs between
// two states.
func changes(before, after map[string]any) string {
	var names []string
	for k, v := range after {
		if old, ok := before[k]; k != "event" && (!ok || fmt.Sprint(old) != fmt.Sprint(v)) {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "no change"
	}
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s: %s -> %s", k, value(before[k]), value(after[k])))
	}
	return strings.Join(parts, ", ")
}

func value(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	if v == nil {
		return "unset"
	}
	return fmt.Sprint(v)
}
