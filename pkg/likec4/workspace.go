// Package likec4 writes one LikeC4 workspace from the shared IR (VIS-1).
// Every view draws from the same specification, so element kinds look the
// same in every view. Every element and relation carries its evidence kind
// and, when known, the file and line it was taken from (VIS-4). Output is
// deterministic: elements, relations and views are written in sorted order.
package likec4

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"generative-architecture/pkg/ir"
)

// Element kinds of the shared specification.
const (
	KindController = "controller"
	KindResource   = "resource"
	KindProcess    = "process"
	KindQueue      = "queue"
	KindCluster    = "cluster"
	KindBoundary   = "boundary"
	KindStep       = "step"
)

// kindStyles is the style of each element kind in spec.c4.
var kindStyles = map[string][]string{
	KindController: {"shape rectangle", "color primary"},
	KindResource:   {"shape storage", "color green"},
	KindProcess:    {"shape component", "color indigo"},
	KindQueue:      {"shape queue", "color amber"},
	KindCluster:    {"shape rectangle", "color slate", "border dotted"},
	KindBoundary:   {"shape rectangle", "color muted", "border dashed"},
	KindStep:       {"shape rectangle", "color sky"},
}

// evidenceKinds are the tags the specification declares, one per evidence
// kind.
var evidenceKinds = []ir.Evidence{ir.Extracted, ir.Observed, ir.Declared, ir.Assumed, ir.Unresolved}

// Element is one node of the model. ID is a LikeC4 identifier (see ID).
type Element struct {
	ID          string
	Kind        string
	Title       string
	Description string
	Evidence    ir.Evidence
	Source      ir.Source
}

// Relation is one edge of the model.
type Relation struct {
	From, To    string
	Label       string
	Description string
	Evidence    ir.Evidence
	Source      ir.Source
}

// Step is one step of a dynamic view, between two elements.
type Step struct {
	From, To    string
	Label       string
	Description string
}

// View is a static view that includes the listed elements, or, when Dynamic
// is set, a dynamic view that plays Steps in order.
type View struct {
	Name    string
	Title   string
	Include []string
	Dynamic bool
	Steps   []Step
}

// Workspace is everything one LikeC4 workspace holds.
type Workspace struct {
	Elements  []Element
	Relations []Relation
	Views     []View
}

// Add merges the elements, relations and view of one generator into the
// workspace. An element already present must be identical; an identical
// relation is kept once.
func (w *Workspace) Add(elements []Element, relations []Relation, view View) error {
	byID := map[string]Element{}
	for _, e := range w.Elements {
		byID[e.ID] = e
	}
	for _, e := range elements {
		if prior, ok := byID[e.ID]; ok {
			if prior != e {
				return fmt.Errorf("element %s is defined twice with different content", e.ID)
			}
			continue
		}
		byID[e.ID] = e
		w.Elements = append(w.Elements, e)
	}
	seen := map[Relation]bool{}
	for _, r := range w.Relations {
		seen[r] = true
	}
	for _, r := range relations {
		if !seen[r] {
			seen[r] = true
			w.Relations = append(w.Relations, r)
		}
	}
	w.Views = append(w.Views, view)
	return nil
}

// ID turns any string into a LikeC4 identifier: every character other than
// an ASCII letter, digit or underscore becomes an underscore, and an
// identifier that would not start with a letter gets the prefix "e_".
func ID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || !(out[0] >= 'a' && out[0] <= 'z' || out[0] >= 'A' && out[0] <= 'Z') {
		out = "e_" + out
	}
	return out
}

// Validate checks that identifiers are unique and well formed, kinds are
// known, every element and relation carries a valid evidence kind, and
// relations, view includes and dynamic steps name existing elements.
func (w Workspace) Validate() error {
	ids := map[string]bool{}
	for _, e := range w.Elements {
		if e.ID == "" || ID(e.ID) != e.ID {
			return fmt.Errorf("element %q: invalid identifier", e.ID)
		}
		if ids[e.ID] {
			return fmt.Errorf("element %s defined twice", e.ID)
		}
		ids[e.ID] = true
		if _, ok := kindStyles[e.Kind]; !ok {
			return fmt.Errorf("element %s: unknown kind %q", e.ID, e.Kind)
		}
		if err := e.Evidence.Validate(); err != nil {
			return fmt.Errorf("element %s: %w", e.ID, err)
		}
	}
	for _, r := range w.Relations {
		if !ids[r.From] || !ids[r.To] {
			return fmt.Errorf("relation %s -> %s names an unknown element", r.From, r.To)
		}
		if err := r.Evidence.Validate(); err != nil {
			return fmt.Errorf("relation %s -> %s: %w", r.From, r.To, err)
		}
	}
	views := map[string]bool{}
	for _, v := range w.Views {
		if v.Name == "" || ID(v.Name) != v.Name {
			return fmt.Errorf("view %q: invalid identifier", v.Name)
		}
		if views[v.Name] {
			return fmt.Errorf("view %s defined twice", v.Name)
		}
		views[v.Name] = true
		for _, id := range v.Include {
			if !ids[id] {
				return fmt.Errorf("view %s includes unknown element %s", v.Name, id)
			}
		}
		for _, s := range v.Steps {
			if !ids[s.From] || !ids[s.To] {
				return fmt.Errorf("view %s: step %s -> %s names an unknown element", v.Name, s.From, s.To)
			}
		}
		if v.Dynamic != (len(v.Steps) > 0) || (v.Dynamic && len(v.Include) > 0) {
			return fmt.Errorf("view %s: a dynamic view has steps and no includes; a static view has no steps", v.Name)
		}
	}
	return nil
}

// Files renders the workspace: spec.c4, model.c4 and views/<name>.c4 for
// each view, keyed by path relative to the workspace directory.
func (w Workspace) Files() (map[string][]byte, error) {
	if err := w.Validate(); err != nil {
		return nil, err
	}
	out := map[string][]byte{"spec.c4": spec(), "model.c4": w.model()}
	for _, v := range w.Views {
		out[filepath.Join("views", v.Name+".c4")] = view(v)
	}
	return out, nil
}

// Source renders the workspace as one .c4 file: the specification, the
// model and every view in name order. It serves callers that place the
// workspace beside other LikeC4 files.
func (w Workspace) Source() ([]byte, error) {
	files, err := w.Files()
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(header)
	for _, name := range sortedKeys(files) {
		b.Write(bytes.TrimPrefix(files[name], []byte(header)))
	}
	return b.Bytes(), nil
}

// Write writes the workspace into dir. Stale view files from an earlier
// write are removed, so the directory always matches the workspace.
func (w Workspace) Write(dir string) error {
	files, err := w.Files()
	if err != nil {
		return err
	}
	old, err := filepath.Glob(filepath.Join(dir, "views", "*.c4"))
	if err != nil {
		return err
	}
	for _, path := range old {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(files) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			return err
		}
	}
	return nil
}

const header = "// Code generated by pkg/likec4. DO NOT EDIT.\n\n"

func spec() []byte {
	var b bytes.Buffer
	b.WriteString(header)
	b.WriteString("specification {\n")
	for _, kind := range sortedKeys(kindStyles) {
		fmt.Fprintf(&b, "  element %s {\n    style {\n", kind)
		for _, s := range kindStyles[kind] {
			fmt.Fprintf(&b, "      %s\n", s)
		}
		b.WriteString("    }\n  }\n")
	}
	for _, e := range evidenceKinds {
		fmt.Fprintf(&b, "  tag %s\n", e)
	}
	b.WriteString("}\n")
	return b.Bytes()
}

func (w Workspace) model() []byte {
	elements := append([]Element(nil), w.Elements...)
	sort.Slice(elements, func(i, j int) bool { return elements[i].ID < elements[j].ID })
	relations := append([]Relation(nil), w.Relations...)
	sort.Slice(relations, func(i, j int) bool { return relationKey(relations[i]) < relationKey(relations[j]) })
	var b bytes.Buffer
	b.WriteString(header)
	b.WriteString("model {\n")
	for _, e := range elements {
		fmt.Fprintf(&b, "  %s = %s %s {\n", e.ID, e.Kind, quote(e.Title))
		body(&b, e.Description, e.Evidence, e.Source)
		b.WriteString("  }\n")
	}
	for _, r := range relations {
		fmt.Fprintf(&b, "  %s -> %s %s {\n", r.From, r.To, quote(r.Label))
		body(&b, r.Description, r.Evidence, r.Source)
		b.WriteString("  }\n")
	}
	b.WriteString("}\n")
	return b.Bytes()
}

// body writes the evidence tag, a description that ends with the evidence
// word and the source location, and both again as metadata.
func body(b *bytes.Buffer, description string, evidence ir.Evidence, source ir.Source) {
	fmt.Fprintf(b, "    #%s\n", evidence)
	text := []string{}
	if description != "" {
		text = append(text, description)
	}
	text = append(text, "evidence: "+string(evidence))
	location := Location(source)
	if location != "" {
		text = append(text, "source: "+location)
	}
	fmt.Fprintf(b, "    description %s\n", quote(strings.Join(text, "; ")))
	fmt.Fprintf(b, "    metadata {\n      evidence %s\n", quote(string(evidence)))
	if location != "" {
		fmt.Fprintf(b, "      source %s\n", quote(location))
	}
	b.WriteString("    }\n")
}

// Location renders a source as file:line, file alone without a line, and
// the empty string when no file is known.
func Location(s ir.Source) string {
	switch {
	case s.File == "":
		return ""
	case s.Line > 0:
		return s.File + ":" + strconv.Itoa(s.Line)
	}
	return s.File
}

func view(v View) []byte {
	var b bytes.Buffer
	b.WriteString(header)
	b.WriteString("views {\n")
	if v.Dynamic {
		fmt.Fprintf(&b, "  dynamic view %s {\n    title %s\n", v.Name, quote(v.Title))
		for _, s := range v.Steps {
			fmt.Fprintf(&b, "    %s -> %s %s", s.From, s.To, quote(s.Label))
			if s.Description != "" {
				fmt.Fprintf(&b, " {\n      notes %s\n    }", quote(s.Description))
			}
			b.WriteString("\n")
		}
	} else {
		include := append([]string(nil), v.Include...)
		sort.Strings(include)
		fmt.Fprintf(&b, "  view %s {\n    title %s\n", v.Name, quote(v.Title))
		if len(include) > 0 {
			fmt.Fprintf(&b, "    include %s\n", strings.Join(include, ", "))
		}
	}
	b.WriteString("  }\n}\n")
	return b.Bytes()
}

// quote renders s as a LikeC4 string literal. Line breaks become spaces.
func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\r", " ", "\n", " ", "\t", " ")
	return "'" + r.Replace(s) + "'"
}

func relationKey(r Relation) string {
	return strings.Join([]string{r.From, r.To, r.Label, r.Description}, "\x00")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
