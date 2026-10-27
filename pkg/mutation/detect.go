package mutation

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/model"
)

// Detector is one way to detect a mutant, such as rebuilding the model and
// checking its constraints, or replaying evidence through the mutated
// implementation. Detect receives the mutated files keyed by absolute
// path, the form go/packages and the go command's -overlay flag take. It
// returns the names of the constraints or variables that reveal the
// mutant, and nothing for a mutant it cannot see.
type Detector struct {
	Name   string
	Detect func(overlay map[string][]byte) ([]string, error)
}

// Detections records, for each mutation and each name reported for it,
// the detectors that reported the name, in detector order.
type Detections map[string]map[string][]string

// Overlay applies m and returns the mutated file keyed by its absolute
// path.
func (s Suite) Overlay(m Mutation) (map[string][]byte, error) {
	contents, err := Apply(s.Root, m)
	if err != nil {
		return nil, err
	}
	path, err := filepath.Abs(filepath.Join(s.Root, filepath.FromSlash(m.File)))
	if err != nil {
		return nil, fmt.Errorf("mutation %s: %w", m.Name, err)
	}
	return map[string][]byte{path: contents}, nil
}

// RunDetectors applies each mutation as an overlay and runs every
// detector on it. The matrix holds the union of what the detectors
// reported, as Run's does. The detections say which detector reported
// each name. A detector error fails the run: it proves nothing about the
// constraints.
func RunDetectors(s Suite, detectors ...Detector) (Matrix, Detections, error) {
	if err := s.Validate(); err != nil {
		return nil, nil, err
	}
	if len(detectors) == 0 {
		return nil, nil, fmt.Errorf("mutation suite %s: at least one detector is required", s.Name)
	}
	seen := map[string]bool{}
	for _, d := range detectors {
		if !name.MatchString(d.Name) || seen[d.Name] || d.Detect == nil {
			return nil, nil, fmt.Errorf("mutation suite %s: detector %q needs a unique identifier name and a Detect function", s.Name, d.Name)
		}
		seen[d.Name] = true
	}
	matrix, detections := Matrix{}, Detections{}
	for _, m := range s.Spec.Mutations {
		overlay, err := s.Overlay(m)
		if err != nil {
			return nil, nil, err
		}
		row, by := map[string]bool{}, map[string][]string{}
		for _, c := range m.Expect {
			row[c] = false
		}
		for _, d := range detectors {
			names, err := d.Detect(overlay)
			if err != nil {
				return nil, nil, fmt.Errorf("mutation %s: detector %s: %w", m.Name, d.Name, err)
			}
			for _, c := range names {
				row[c] = true
				if !slices.Contains(by[c], d.Name) {
					by[c] = append(by[c], d.Name)
				}
			}
		}
		matrix[m.Name], detections[m.Name] = row, by
	}
	return matrix, detections, nil
}

// CaughtBy names, for each mutation, the detectors that reported at least
// one of its expected constraints, sorted. A mutation no detector caught
// has an empty entry.
func (s Suite) CaughtBy(d Detections) map[string][]string {
	out := map[string][]string{}
	for _, m := range s.Spec.Mutations {
		by := []string{}
		for _, c := range m.Expect {
			for _, name := range d[m.Name][c] {
				if !slices.Contains(by, name) {
					by = append(by, name)
				}
			}
		}
		sort.Strings(by)
		out[m.Name] = by
	}
	return out
}

// String renders one line per mutation with each reported name and its
// detectors, as name[detector,detector].
func (d Detections) String() string {
	mutations := make([]string, 0, len(d))
	for m := range d {
		mutations = append(mutations, m)
	}
	sort.Strings(mutations)
	var b strings.Builder
	for _, m := range mutations {
		names := make([]string, 0, len(d[m]))
		for n := range d[m] {
			names = append(names, n)
		}
		sort.Strings(names)
		b.WriteString(m + ":")
		for _, n := range names {
			b.WriteString(" " + n + "[" + strings.Join(d[m][n], ",") + "]")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Rebuild generates the model of a mutant from the overlaid source. It
// returns the model with the traces of it to check, such as targeted
// scenarios.
type Rebuild func(overlay map[string][]byte) (*model.Compiled, []conformance.Trace, error)

// ModelDetector is the detector named "model". It rebuilds the model from
// the mutated source and names every property of the rebuilt model that a
// state of the returned traces violates. A rebuild with no traces is an
// error, since it could detect nothing.
func ModelDetector(rebuild Rebuild) Detector {
	return Detector{Name: "model", Detect: func(overlay map[string][]byte) ([]string, error) {
		m, traces, err := rebuild(overlay)
		if err != nil {
			return nil, err
		}
		if len(traces) == 0 {
			return nil, fmt.Errorf("the rebuilt model has no traces to check")
		}
		found := map[string]bool{}
		for i, t := range traces {
			for j, s := range t.States {
				violated, err := m.Violations(s.Values, s.Event)
				if err != nil {
					return nil, fmt.Errorf("trace %d state %d (%s): %w", i, j, s.Event, err)
				}
				for _, p := range violated {
					found[p] = true
				}
			}
		}
		out := make([]string, 0, len(found))
		for p := range found {
			out = append(out, p)
		}
		sort.Strings(out)
		return out, nil
	}}
}
