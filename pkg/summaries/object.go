package summaries

import (
	"fmt"
	"slices"
	"strings"
)

// ObjectSummary describes a library function that changes a Go object in
// memory without calling the API server, such as setting an owner
// reference. The function takes the object it reads at argument Source and
// the object it writes at argument Target, followed by Fixed further
// arguments; any argument beyond those (a variadic option) is not
// summarized.
//
// Writes give the fields set on the target, by JSON path, with a CEL
// template for each value. A template may use the placeholders that Reads
// binds to JSON paths of the source object. Unvalued lists fields the
// function also sets but the summary gives no value: a caller that models
// one of them must fail. The function is assumed to succeed; its error
// result is false.
type ObjectSummary struct {
	Package  string
	Function string
	Source   int
	Target   int
	Fixed    int
	Reads    map[string][]string
	Writes   []FieldWrite
	Unvalued [][]string
}

// FieldWrite sets the field at Path of the target to the CEL template
// Value.
type FieldWrite struct {
	Path  []string
	Value string
}

// controllerutilPackage declares the owner reference helpers.
const controllerutilPackage = "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

// ownerReference is the JSON path of the owner reference of an object.
// The abstraction addresses list entries by scalar paths, so the path
// stands for the single owner reference the helpers write.
var ownerReference = []string{"metadata", "ownerReferences"}

func ownerField(name string) []string { return append(slices.Clone(ownerReference), name) }

// ownerReads binds the owner's name and UID.
var ownerReads = map[string][]string{
	"$ownerName": {"metadata", "name"},
	"$ownerUID":  {"metadata", "uid"},
}

// The API version and kind come from the scheme, which the extractor does
// not read.
var ownerUnvalued = [][]string{ownerField("apiVersion"), ownerField("kind")}

// objectSummaries are the built-in object summaries.
var objectSummaries = []ObjectSummary{
	{
		Package: controllerutilPackage, Function: "SetControllerReference", Source: 0, Target: 1, Fixed: 1,
		Reads: ownerReads,
		Writes: []FieldWrite{
			{ownerField("name"), "$ownerName"},
			{ownerField("uid"), "$ownerUID"},
			{ownerField("controller"), "true"},
			{ownerField("blockOwnerDeletion"), "true"},
		},
		Unvalued: ownerUnvalued,
	},
	{
		Package: controllerutilPackage, Function: "SetOwnerReference", Source: 0, Target: 1, Fixed: 1,
		Reads: ownerReads,
		Writes: []FieldWrite{
			{ownerField("name"), "$ownerName"},
			{ownerField("uid"), "$ownerUID"},
			{ownerField("controller"), "false"},
			{ownerField("blockOwnerDeletion"), "false"},
		},
		Unvalued: ownerUnvalued,
	},
}

// ObjectSummaryFor returns the built-in summary of the function name
// declared in package pkg.
func ObjectSummaryFor(pkg, name string) (ObjectSummary, bool) {
	for _, s := range objectSummaries {
		if s.Package == pkg && s.Function == name {
			return s, true
		}
	}
	return ObjectSummary{}, false
}

// Validate checks the argument positions and that every placeholder a
// write uses is bound by Reads.
func (s ObjectSummary) Validate() error {
	name := s.Package + "." + s.Function
	if s.Source < 0 || s.Target < 0 || s.Source == s.Target || s.Fixed < 0 {
		return fmt.Errorf("object summary %s: invalid argument positions", name)
	}
	for p, path := range s.Reads {
		if placeholder.FindString(p) != p || len(path) == 0 {
			return fmt.Errorf("object summary %s: invalid read %s", name, p)
		}
	}
	seen := map[string]bool{}
	for _, w := range s.Writes {
		key := strings.Join(w.Path, ".")
		if len(w.Path) == 0 || seen[key] || w.Value == "" {
			return fmt.Errorf("object summary %s: invalid write %s", name, key)
		}
		seen[key] = true
		for _, p := range Placeholders(w.Value) {
			if _, ok := s.Reads[p]; !ok {
				return fmt.Errorf("object summary %s: write %s uses unbound placeholder %s", name, key, p)
			}
		}
	}
	for _, path := range s.Unvalued {
		if seen[strings.Join(path, ".")] {
			return fmt.Errorf("object summary %s: %s is both written and unvalued", name, strings.Join(path, "."))
		}
	}
	return nil
}
