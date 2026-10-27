// Package report defines the result documents the tools write for adopters.
// A SupportReport says which constructs of a project were included in a
// model, which were summarized, and which are unsupported, each with a source
// location, so a project can see what the extractor covered before trusting a
// generated model.
package report

import (
	"fmt"
	"sort"

	"github.com/phoban01/generative-architecture/pkg/krm"
)

// Kind is the KRM kind of the document written by Support.Write.
const Kind = "SupportReport"

// Location is a position in a source file. Line is 1-based; 0 means unknown.
type Location struct {
	File string `yaml:"file"`
	Line int    `yaml:"line"`
}

// Item is one construct the extractor classified.
type Item struct {
	Kind      string   `yaml:"kind"`
	Construct string   `yaml:"construct"`
	Reason    string   `yaml:"reason,omitempty"`
	Location  Location `yaml:"location"`
}

// Support classifies the constructs of a project into three buckets and
// lists the facts whose evidence is unresolved (EVID-3). Inventory mode
// reports unsupported and unresolved items; verification mode fails on
// them.
type Support struct {
	// Name is the document's metadata.name; empty means "support".
	Name        string `yaml:"-"`
	Included    []Item `yaml:"included"`
	Summarized  []Item `yaml:"summarized"`
	Unsupported []Item `yaml:"unsupported"`
	Unresolved  []Item `yaml:"unresolved,omitempty"`
}

// Blocking lists the unsupported and unresolved items that inScope
// accepts: what verification mode fails on.
func (s Support) Blocking(inScope func(Location) bool) []Item {
	var out []Item
	for _, items := range [][]Item{s.Unsupported, s.Unresolved} {
		for _, it := range items {
			if inScope(it.Location) {
				out = append(out, it)
			}
		}
	}
	return out
}

// Buckets are the accepted values of the kind argument to Add.
const (
	Included    = "included"
	Summarized  = "summarized"
	Unsupported = "unsupported"
	Unresolved  = "unresolved"
)

// Add places item in the named bucket.
func (s *Support) Add(kind string, item Item) error {
	switch kind {
	case Included:
		s.Included = append(s.Included, item)
	case Summarized:
		s.Summarized = append(s.Summarized, item)
	case Unsupported:
		s.Unsupported = append(s.Unsupported, item)
	case Unresolved:
		s.Unresolved = append(s.Unresolved, item)
	default:
		return fmt.Errorf("unknown support bucket %q; expected included, summarized, unsupported or unresolved", kind)
	}
	return nil
}

// Sort orders every bucket by file, line and construct so the written
// document is deterministic regardless of extraction order.
func (s *Support) Sort() {
	for _, items := range [][]Item{s.Included, s.Summarized, s.Unsupported, s.Unresolved} {
		sort.SliceStable(items, func(i, j int) bool {
			a, b := items[i], items[j]
			if a.Location.File != b.Location.File {
				return a.Location.File < b.Location.File
			}
			if a.Location.Line != b.Location.Line {
				return a.Location.Line < b.Location.Line
			}
			return a.Construct < b.Construct
		})
	}
}

// Write sorts the report and writes it as a SupportReport status document.
func (s *Support) Write(path string) error {
	s.Sort()
	name := s.Name
	if name == "" {
		name = "support"
	}
	out := *s
	for _, p := range []*[]Item{&out.Included, &out.Summarized, &out.Unsupported} {
		if *p == nil {
			*p = []Item{}
		}
	}
	return krm.WriteResult(path, Kind, name, out)
}

// Read loads a SupportReport written by Write.
func Read(path string) (Support, error) {
	var s Support
	err := krm.ReadResult(path, Kind, &s)
	return s, err
}
