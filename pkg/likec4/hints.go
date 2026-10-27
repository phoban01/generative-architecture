package likec4

import (
	"fmt"
	"os"
	"slices"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/phoban01/generative-architecture/pkg/krm"
)

// HintsKind is the KRM kind of a presentation hints document.
const HintsKind = "ViewHints"

// Hints are authored presentation hints for a workspace (VIS-3). They
// change titles, frame elements in groups and set the layout direction of
// views. They never add or remove elements or relations.
type Hints struct {
	// Titles maps an element ID or a view name to its title.
	Titles map[string]string `yaml:"titles,omitempty"`
	// Groups maps a group title to the IDs of the elements it frames. Each
	// static view frames the members it includes; an element belongs to
	// at most one group.
	Groups map[string][]string `yaml:"groups,omitempty"`
	// Layout maps a view name to one of Layouts.
	Layout map[string]string `yaml:"layout,omitempty"`

	// Path is the document the hints were read from, for diagnostics.
	Path string `yaml:"-"`
	// lines maps a field path such as "spec.titles.mirror" to its line in
	// Path.
	lines map[string]int
}

// LoadHints reads a ViewHints document strictly: unknown fields, duplicate
// keys and a wrong kind fail. It records the line of every hint, so
// Validate can locate its diagnostics.
func LoadHints(path string) (Hints, error) {
	var h Hints
	if _, err := krm.Read(path, HintsKind, &h); err != nil {
		return h, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return h, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return h, fmt.Errorf("%s: %w", path, err)
	}
	h.Path = path
	h.lines = map[string]int{}
	spec := mapValue(&doc, "spec")
	for _, field := range []string{"titles", "groups", "layout"} {
		m := mapValue(spec, field)
		if m == nil || m.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			key, value := m.Content[i], m.Content[i+1]
			at := "spec." + field + "." + key.Value
			h.lines[at] = key.Line
			if value.Kind == yaml.SequenceNode {
				for j, item := range value.Content {
					h.lines[at+"["+strconv.Itoa(j)+"]"] = item.Line
				}
			}
		}
	}
	return h, nil
}

// mapValue returns the value of key in the mapping n, or in the mapping of
// the document n. It returns nil when there is none.
func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// at locates a field path: file:line when known, the file alone, or the
// field path alone for hints built in code.
func (h Hints) at(field string) string {
	switch {
	case h.Path == "":
		return field
	case h.lines[field] > 0:
		return fmt.Sprintf("%s:%d: %s", h.Path, h.lines[field], field)
	}
	return h.Path + ": " + field
}

// Validate checks the hints against w: every title names one element or
// one view, every group member is a known element in no other group, every
// group has a title and members, and every layout names a known view and
// direction. Each diagnostic carries the hint's location.
func (h Hints) Validate(w Workspace) error {
	elements := map[string]bool{}
	for _, e := range w.Elements {
		elements[e.ID] = true
	}
	views := map[string]bool{}
	for _, v := range w.Views {
		views[v.Name] = true
	}
	for _, id := range sortedKeys(h.Titles) {
		at := h.at("spec.titles." + id)
		switch {
		case elements[id] && views[id]:
			return fmt.Errorf("%s: %s names both an element and a view", at, id)
		case !elements[id] && !views[id]:
			return fmt.Errorf("%s: unknown element or view %s", at, id)
		case h.Titles[id] == "":
			return fmt.Errorf("%s: empty title", at)
		}
	}
	member := map[string]string{}
	for _, name := range sortedKeys(h.Groups) {
		at := h.at("spec.groups." + name)
		if name == "" || len(h.Groups[name]) == 0 {
			return fmt.Errorf("%s: a group needs a title and elements", at)
		}
		for i, id := range h.Groups[name] {
			at := h.at("spec.groups." + name + "[" + strconv.Itoa(i) + "]")
			if !elements[id] {
				return fmt.Errorf("%s: unknown element %s", at, id)
			}
			if other, ok := member[id]; ok {
				return fmt.Errorf("%s: element %s is already in group %q", at, id, other)
			}
			member[id] = name
		}
	}
	for _, name := range sortedKeys(h.Layout) {
		at := h.at("spec.layout." + name)
		if !views[name] {
			return fmt.Errorf("%s: unknown view %s", at, name)
		}
		if !slices.Contains(Layouts, h.Layout[name]) {
			return fmt.Errorf("%s: unknown layout %q; expected one of %v", at, h.Layout[name], Layouts)
		}
	}
	return nil
}

// Apply validates h against w and then applies it: it sets titles, frames
// the grouped elements of each static view and sets view layouts. It
// changes nothing when validation fails. It never adds or removes an
// element, a relation or a view.
func Apply(w *Workspace, h Hints) error {
	if err := h.Validate(*w); err != nil {
		return err
	}
	for i := range w.Elements {
		if t, ok := h.Titles[w.Elements[i].ID]; ok {
			w.Elements[i].Title = t
		}
	}
	for i := range w.Views {
		v := &w.Views[i]
		if t, ok := h.Titles[v.Name]; ok {
			v.Title = t
		}
		if l, ok := h.Layout[v.Name]; ok {
			v.Layout = l
		}
		if v.Dynamic || len(h.Groups) == 0 {
			continue
		}
		v.Groups = nil
		for _, name := range sortedKeys(h.Groups) {
			var members []string
			for _, id := range h.Groups[name] {
				if slices.Contains(v.Include, id) {
					members = append(members, id)
				}
			}
			if len(members) > 0 {
				v.Groups = append(v.Groups, Group{Title: name, Include: members})
			}
		}
	}
	return w.Validate()
}
