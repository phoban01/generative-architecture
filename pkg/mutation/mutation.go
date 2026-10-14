// Package mutation keeps the mutation suite as data (CON-8): each entry
// names a file, a textual find/replace and the constraints expected to
// detect the change. Apply produces mutated contents in memory and Run
// builds a detection matrix with the caller's build and check steps.
package mutation

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"generative-architecture/pkg/krm"
)

// Kind is the KRM kind of a mutation suite document.
const Kind = "MutationSuite"

var name = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// Mutation is one textual change to one file and the constraints that
// should detect it.
type Mutation struct {
	Name    string   `yaml:"name"`
	File    string   `yaml:"file"`
	Find    string   `yaml:"find"`
	Replace string   `yaml:"replace"`
	Expect  []string `yaml:"expect"`
}

// Spec is the payload of a MutationSuite document.
type Spec struct {
	Mutations []Mutation `yaml:"mutations"`
}

// Suite is a loaded document. Root is the directory mutation files are
// relative to: the document's directory when loaded from disk.
type Suite struct {
	Name string
	Root string
	Spec Spec
}

// Matrix is the detection matrix: for each mutation, which constraints
// reported a violation. Expected constraints are always present; other
// constraints that fired are added as true.
type Matrix map[string]map[string]bool

// Load reads and validates a MutationSuite document.
func Load(path string) (Suite, error) {
	var s Suite
	meta, err := krm.Read(path, Kind, &s.Spec)
	if err != nil {
		return s, err
	}
	s.Name = meta.Name
	s.Root = filepath.Dir(path)
	if err = s.Validate(); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Validate checks names, files, patterns and expectations.
func (s Suite) Validate() error {
	if len(s.Spec.Mutations) == 0 {
		return fmt.Errorf("mutation suite %s: at least one mutation is required", s.Name)
	}
	seen := map[string]bool{}
	for _, m := range s.Spec.Mutations {
		if !name.MatchString(m.Name) {
			return fmt.Errorf("mutation suite %s: mutation name %q must be an identifier", s.Name, m.Name)
		}
		if seen[m.Name] {
			return fmt.Errorf("mutation suite %s: mutation %s declared twice", s.Name, m.Name)
		}
		seen[m.Name] = true
		if m.File == "" || filepath.IsAbs(m.File) || strings.HasPrefix(filepath.ToSlash(m.File), "../") {
			return fmt.Errorf("mutation %s: file %q must be relative to the suite", m.Name, m.File)
		}
		if m.Find == "" {
			return fmt.Errorf("mutation %s: find must not be empty", m.Name)
		}
		if m.Find == m.Replace {
			return fmt.Errorf("mutation %s: replace equals find", m.Name)
		}
		if len(m.Expect) == 0 {
			return fmt.Errorf("mutation %s: expect at least one constraint", m.Name)
		}
		expected := map[string]bool{}
		for _, c := range m.Expect {
			if c == "" || expected[c] {
				return fmt.Errorf("mutation %s: empty or duplicate expected constraint %q", m.Name, c)
			}
			expected[c] = true
		}
	}
	return nil
}

// Apply returns the contents of root/m.File with every occurrence of find
// replaced. Nothing is written to disk. A pattern that does not occur is an
// error: the mutation would be a no-op.
func Apply(root string, m Mutation) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(m.File)))
	if err != nil {
		return nil, fmt.Errorf("mutation %s: %w", m.Name, err)
	}
	if !bytes.Contains(b, []byte(m.Find)) {
		return nil, fmt.Errorf("mutation %s: %q does not occur in %s", m.Name, m.Find, m.File)
	}
	return bytes.ReplaceAll(b, []byte(m.Find), []byte(m.Replace)), nil
}

// Run applies each mutation, hands the mutated contents (file path to
// bytes) to build, then asks check for the names of the violated
// constraints. A mutation whose build fails is an error: it proves nothing
// about the constraints.
func Run(s Suite, build func(mutated map[string][]byte) error, check func() []string) (Matrix, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	matrix := Matrix{}
	for _, m := range s.Spec.Mutations {
		contents, err := Apply(s.Root, m)
		if err != nil {
			return nil, err
		}
		if err = build(map[string][]byte{m.File: contents}); err != nil {
			return nil, fmt.Errorf("mutation %s: build: %w", m.Name, err)
		}
		row := map[string]bool{}
		for _, c := range m.Expect {
			row[c] = false
		}
		for _, c := range check() {
			row[c] = true
		}
		matrix[m.Name] = row
	}
	return matrix, nil
}

// Escaped lists the mutations with an expected constraint that did not
// fire, as "mutation: constraint" in order.
func (s Suite) Escaped(matrix Matrix) []string {
	var out []string
	for _, m := range s.Spec.Mutations {
		for _, c := range m.Expect {
			if !matrix[m.Name][c] {
				out = append(out, m.Name+": "+c)
			}
		}
	}
	return out
}

// Uncovered lists the given constraints that no mutation expects to
// detect (CON-8 requires at least one per constraint).
func (s Suite) Uncovered(constraints []string) []string {
	expected := map[string]bool{}
	for _, m := range s.Spec.Mutations {
		for _, c := range m.Expect {
			expected[c] = true
		}
	}
	var out []string
	for _, c := range constraints {
		if !expected[c] {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// String renders the matrix as one line per mutation with the constraints
// in order and + for detected, - for undetected.
func (m Matrix) String() string {
	mutations := make([]string, 0, len(m))
	for name := range m {
		mutations = append(mutations, name)
	}
	sort.Strings(mutations)
	var b strings.Builder
	for _, name := range mutations {
		constraints := make([]string, 0, len(m[name]))
		for c := range m[name] {
			constraints = append(constraints, c)
		}
		sort.Strings(constraints)
		b.WriteString(name + ":")
		for _, c := range constraints {
			mark := "-"
			if m[name][c] {
				mark = "+"
			}
			b.WriteString(" " + mark + c)
		}
		b.WriteString("\n")
	}
	return b.String()
}
