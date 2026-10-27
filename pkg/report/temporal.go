package report

import (
	"fmt"
	"io"
	"strings"
)

// Statuses of a temporal property (CON-6). Sampling never sets
// TemporalVerified: only a completed model-checking run under a declared
// fairness assumption does.
const (
	TemporalVerified  = "verified"
	TemporalViolated  = "violated"
	TemporalUnchecked = "unchecked"
)

// TemporalResult is the status of one liveness property. Weak and Strong
// list the actions assumed weakly and strongly fair. Backend and Bound
// state what a verified or violated result covers. Reason says why an
// unchecked property was not checked.
type TemporalResult struct {
	Name    string   `yaml:"name"`
	Status  string   `yaml:"status"`
	Weak    []string `yaml:"weak,omitempty"`
	Strong  []string `yaml:"strong,omitempty"`
	Backend string   `yaml:"backend,omitempty"`
	Bound   string   `yaml:"bound,omitempty"`
	Reason  string   `yaml:"reason,omitempty"`
}

// validateTemporal requires a known status, a fairness assumption, a
// backend and a bound for each checked property, and a reason for each
// unchecked one.
func (v Verification) validateTemporal() error {
	for _, t := range v.Temporal {
		switch t.Status {
		case TemporalVerified, TemporalViolated:
			if len(t.Weak)+len(t.Strong) == 0 || t.Backend == "" || t.Bound == "" {
				return fmt.Errorf("verification report: temporal property %s is %s without fairness, backend and bound", t.Name, t.Status)
			}
		case TemporalUnchecked:
			if t.Reason == "" {
				return fmt.Errorf("verification report: temporal property %s is unchecked without a reason", t.Name)
			}
		default:
			return fmt.Errorf("verification report: temporal property %s has unknown status %q", t.Name, t.Status)
		}
	}
	return nil
}

// WriteTemporal prints one line per temporal property.
func (v Verification) WriteTemporal(w io.Writer) {
	if len(v.Temporal) == 0 {
		return
	}
	fmt.Fprintln(w, "temporal properties:")
	for _, t := range v.Temporal {
		if t.Status == TemporalUnchecked {
			fmt.Fprintf(w, "  %s: unchecked (%s)\n", t.Name, t.Reason)
			continue
		}
		var fair []string
		if len(t.Weak) > 0 {
			fair = append(fair, "weak("+strings.Join(t.Weak, ", ")+")")
		}
		if len(t.Strong) > 0 {
			fair = append(fair, "strong("+strings.Join(t.Strong, ", ")+")")
		}
		fmt.Fprintf(w, "  %s: %s with %s under fairness %s; bound: %s\n", t.Name, t.Status, t.Backend, strings.Join(fair, " "), t.Bound)
	}
}
