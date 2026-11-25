package conformance

import (
	"encoding/json"
	"fmt"
	"sort"
)

// WriteITF encodes a trace in the Informal Trace Format as ReadITF reads it:
// integers as #bigint objects, strings and booleans as JSON values, and
// the event of each state under "event". The variables are the sorted
// union of the state variables and "event". A trace needs a status and at
// least one state.
func WriteITF(t Trace) ([]byte, error) {
	if t.Status == "" || len(t.States) == 0 {
		return nil, fmt.Errorf("itf: a trace needs a status and states")
	}
	names := map[string]bool{"event": true}
	for _, s := range t.States {
		for k := range s.Values {
			names[k] = true
		}
	}
	vars := make([]string, 0, len(names))
	for k := range names {
		vars = append(vars, k)
	}
	sort.Strings(vars)
	states := make([]map[string]any, 0, len(t.States))
	for i, s := range t.States {
		state := map[string]any{"#meta": map[string]any{"index": i}, "event": s.Event}
		for k, v := range s.Values {
			switch x := v.(type) {
			case int64:
				state[k] = map[string]string{"#bigint": fmt.Sprintf("%d", x)}
			case int:
				state[k] = map[string]string{"#bigint": fmt.Sprintf("%d", x)}
			case string, bool:
				state[k] = x
			default:
				return nil, fmt.Errorf("itf: state %d: %s has type %T", i, k, v)
			}
		}
		states = append(states, state)
	}
	doc := map[string]any{
		"#meta":  map[string]any{"format": "ITF", "format-description": "https://apalache-mc.org/docs/adr/015adr-trace.html", "status": t.Status},
		"vars":   vars,
		"states": states,
	}
	return json.Marshal(doc)
}
