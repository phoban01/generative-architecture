// Package abstraction loads the Abstraction document: the declared, reviewable
// step from Kubernetes objects to bounded model state (ADR-0005). The same
// document drives model state generation and replay projection.
package abstraction

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
)

// Kind is the KRM kind of an abstraction document.
const Kind = "Abstraction"

// Model types a field or local may take.
const (
	TypeString = "string"
	TypeBool   = "bool"
	TypeInt    = "int"
)

var (
	types = map[string]bool{TypeString: true, TypeBool: true, TypeInt: true}
	// Object ids and path elements carry no underscore so derived names
	// split back unambiguously; locals are ordinary model identifiers.
	identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	localName  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

// Object is one bounded object of a resource that enters the model.
type Object struct {
	ID        string `yaml:"id"`
	Namespace string `yaml:"namespace,omitempty"`
	Name      string `yaml:"name"`
}

// Field selects one JSON path of a resource and gives it a model type.
//
// Path elements are identifiers, except that the element after
// metadata.annotations or metadata.labels is a map key, such as an
// annotation key containing dots and slashes. A field whose path holds such
// a key cannot be named by its path and declares Name instead.
type Field struct {
	Path []string `yaml:"path"`
	Type string   `yaml:"type"`
	// Name, an identifier, names the field in derived variables and in
	// valueDomains in place of its path. It is required when the path holds
	// a map key and optional otherwise.
	Name string `yaml:"name,omitempty"`
	// Equals makes the field a predicate on a string: the value at Path is
	// read as a string and the model sees the bool whether it equals this
	// string literal (written like a valueDomains entry). An absent value
	// does not equal it. A predicate field has type bool.
	Equals string `yaml:"equals,omitempty"`
}

// Key names the field in derived variables and valueDomains: its Name when
// declared, otherwise its path joined with dots.
func (f Field) Key() string {
	if f.Name != "" {
		return f.Name
	}
	return PathKey(f.Path)
}

// Compared is the string a predicate field compares the value at its path
// to; ok is false when the field is not a valid predicate.
func (f Field) Compared() (value string, ok bool) {
	if f.Equals == "" {
		return "", false
	}
	v, err := strconv.Unquote(f.Equals)
	return v, err == nil
}

// Reference says that a field of this resource names an object of another.
type Reference struct {
	Path      []string `yaml:"path"`
	TargetGVK ir.GVK   `yaml:"targetGVK"`
}

// Resource declares which objects and fields of one kind enter the model.
type Resource struct {
	GVK          ir.GVK              `yaml:"gvk"`
	Objects      []Object            `yaml:"objects"`
	Fields       []Field             `yaml:"fields,omitempty"`
	ValueDomains map[string][]string `yaml:"valueDomains,omitempty"`
	Identity     [][]string          `yaml:"identity,omitempty"`
	References   []Reference         `yaml:"references,omitempty"`
}

// Local is a controller-local model variable that is not object state.
type Local struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

// Bounds limit the population and the run length of the model.
type Bounds struct {
	MaxObjectsPerResource int `yaml:"maxObjectsPerResource,omitempty"`
	MaxInvocations        int `yaml:"maxInvocations,omitempty"`
}

// Spec is the payload of an Abstraction document.
type Spec struct {
	Resources []Resource `yaml:"resources"`
	Locals    []Local    `yaml:"locals,omitempty"`
	Bounds    Bounds     `yaml:"bounds,omitempty"`
	// Excludes lists behaviours the abstraction deliberately leaves out, so
	// reports can repeat them.
	Excludes []string `yaml:"excludes,omitempty"`
}

// Abstraction is a loaded document.
type Abstraction struct {
	Name string
	Spec Spec
}

// Load reads and validates an Abstraction document.
func Load(path string) (Abstraction, error) {
	var a Abstraction
	meta, err := krm.Read(path, Kind, &a.Spec)
	if err != nil {
		return a, err
	}
	a.Name = meta.Name
	return a, a.Validate()
}

// PathKey joins a field path the way valueDomains keys of unnamed fields are
// written (see Field.Key).
func PathKey(path []string) string { return strings.Join(path, ".") }

// Validate checks types, populations, domains, references and bounds.
func (a Abstraction) Validate() error {
	s := a.Spec
	if len(s.Resources) == 0 {
		return fmt.Errorf("abstraction %s: at least one resource is required", a.Name)
	}
	known := map[ir.GVK]bool{}
	for _, r := range s.Resources {
		if err := r.GVK.Validate(); err != nil {
			return fmt.Errorf("abstraction %s: %w", a.Name, err)
		}
		if known[r.GVK] {
			return fmt.Errorf("abstraction %s: resource %s declared twice", a.Name, r.GVK)
		}
		known[r.GVK] = true
	}
	seenVars := map[string]bool{}
	for _, r := range s.Resources {
		where := fmt.Sprintf("abstraction %s, resource %s", a.Name, r.GVK)
		if len(r.Objects) == 0 {
			return fmt.Errorf("%s: at least one object is required", where)
		}
		if s.Bounds.MaxObjectsPerResource > 0 && len(r.Objects) > s.Bounds.MaxObjectsPerResource {
			return fmt.Errorf("%s: %d objects exceed bounds.maxObjectsPerResource %d", where, len(r.Objects), s.Bounds.MaxObjectsPerResource)
		}
		ids := map[string]bool{}
		for _, o := range r.Objects {
			if !identifier.MatchString(o.ID) {
				return fmt.Errorf("%s: object id %q must be an identifier", where, o.ID)
			}
			if ids[o.ID] {
				return fmt.Errorf("%s: duplicate object id %q", where, o.ID)
			}
			ids[o.ID] = true
			if o.Name == "" {
				return fmt.Errorf("%s: object %s has no name", where, o.ID)
			}
		}
		paths := map[string]bool{}
		for _, f := range r.Fields {
			key, err := validField(f)
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			if !types[f.Type] {
				return fmt.Errorf("%s: field %s has unknown type %q; expected string, bool or int", where, key, f.Type)
			}
			if paths[key] {
				return fmt.Errorf("%s: field %s declared twice", where, key)
			}
			paths[key] = true
		}
		for key, values := range r.ValueDomains {
			if !paths[key] {
				return fmt.Errorf("%s: valueDomains names undeclared field %s", where, key)
			}
			if len(values) == 0 {
				return fmt.Errorf("%s: value domain of %s is empty", where, key)
			}
		}
		for _, p := range r.Identity {
			if _, err := validPath(p); err != nil {
				return fmt.Errorf("%s: identity: %w", where, err)
			}
		}
		for _, ref := range r.References {
			key, err := validPath(ref.Path)
			if err != nil {
				return fmt.Errorf("%s: reference: %w", where, err)
			}
			if !known[ref.TargetGVK] {
				return fmt.Errorf("%s: reference %s targets undeclared resource %s", where, key, ref.TargetGVK)
			}
		}
		for _, o := range r.Objects {
			for _, name := range r.variableNames(o) {
				if seenVars[name] {
					return fmt.Errorf("%s: derived variable %s collides with another", where, name)
				}
				seenVars[name] = true
			}
		}
	}
	for _, l := range s.Locals {
		if !localName.MatchString(l.Name) {
			return fmt.Errorf("abstraction %s: local %q must be an identifier", a.Name, l.Name)
		}
		if !types[l.Type] {
			return fmt.Errorf("abstraction %s: local %s has unknown type %q", a.Name, l.Name, l.Type)
		}
		if seenVars[l.Name] {
			return fmt.Errorf("abstraction %s: local %s collides with a derived variable", a.Name, l.Name)
		}
		seenVars[l.Name] = true
	}
	if s.Bounds.MaxObjectsPerResource < 0 || s.Bounds.MaxInvocations < 0 {
		return fmt.Errorf("abstraction %s: bounds must not be negative", a.Name)
	}
	return nil
}

// mapFields are the object paths whose next element is a map key rather
// than a field name.
var mapFields = [][]string{{"metadata", "annotations"}, {"metadata", "labels"}}

// validField checks a field's path, name and predicate and returns its key.
func validField(f Field) (string, error) {
	if len(f.Path) == 0 {
		return "", fmt.Errorf("empty field path")
	}
	keyed := false
	for i, p := range f.Path {
		if identifier.MatchString(p) {
			continue
		}
		if p == "" || i != 2 || len(f.Path) != 3 || !isMapField(f.Path[:2]) {
			return "", fmt.Errorf("field path element %q must be an identifier (only the key after metadata.annotations or metadata.labels may be any string)", p)
		}
		keyed = true
	}
	if f.Name != "" && !identifier.MatchString(f.Name) {
		return "", fmt.Errorf("field name %q must be an identifier", f.Name)
	}
	if keyed && f.Name == "" {
		return "", fmt.Errorf("field %s holds a map key and needs a name", strings.Join(f.Path, "."))
	}
	if f.Equals != "" {
		if f.Type != TypeBool {
			return "", fmt.Errorf("field %s: a predicate (equals) has type bool, got %q", f.Key(), f.Type)
		}
		if _, ok := f.Compared(); !ok {
			return "", fmt.Errorf("field %s: equals %s is not a string literal", f.Key(), f.Equals)
		}
	}
	return f.Key(), nil
}

func isMapField(prefix []string) bool {
	for _, m := range mapFields {
		if slices.Equal(prefix, m) {
			return true
		}
	}
	return false
}

func validPath(path []string) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("empty field path")
	}
	for _, p := range path {
		if !identifier.MatchString(p) {
			return "", fmt.Errorf("field path element %q must be an identifier", p)
		}
	}
	return PathKey(path), nil
}

// VariableName is the model variable for one field of one object:
// <kind>_<objectID>_<path elements joined by _>.
func VariableName(kind, objectID string, path []string) string {
	return kind + "_" + objectID + "_" + strings.Join(path, "_")
}

// FieldVariable is the model variable for one declared field of one
// object: <kind>_<objectID>_<name> for a named field, otherwise
// VariableName of the field's path.
func FieldVariable(kind, objectID string, f Field) string {
	if f.Name != "" {
		return kind + "_" + objectID + "_" + f.Name
	}
	return VariableName(kind, objectID, f.Path)
}

// ExistsVariable is the model variable saying whether the object exists.
func ExistsVariable(kind, objectID string) string { return kind + "_" + objectID + "_exists" }

func (r Resource) variableNames(o Object) []string {
	out := []string{ExistsVariable(r.GVK.Kind, o.ID)}
	for _, f := range r.Fields {
		out = append(out, FieldVariable(r.GVK.Kind, o.ID, f))
	}
	return out
}

// Initial is the zero value of a model type as a CEL expression.
func Initial(typ string) string {
	switch typ {
	case TypeBool:
		return "false"
	case TypeInt:
		return "0"
	}
	return `""`
}

// StateVariables derives the model state: for every object an exists flag
// and one variable per declared field, plus one variable per local. Names
// are returned in a map; use SortedVariables for a deterministic order.
func (a Abstraction) StateVariables() map[string]model.Variable {
	out := map[string]model.Variable{}
	for _, r := range a.Spec.Resources {
		for _, o := range r.Objects {
			out[ExistsVariable(r.GVK.Kind, o.ID)] = model.Variable{Type: TypeBool, Initial: expr.Expression("false")}
			for _, f := range r.Fields {
				out[FieldVariable(r.GVK.Kind, o.ID, f)] = model.Variable{Type: f.Type, Initial: expr.Expression(Initial(f.Type))}
			}
		}
	}
	for _, l := range a.Spec.Locals {
		out[l.Name] = model.Variable{Type: l.Type, Initial: expr.Expression(Initial(l.Type))}
	}
	return out
}

// SortedVariables lists the derived variable names in order.
func (a Abstraction) SortedVariables() []string {
	vars := a.StateVariables()
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
