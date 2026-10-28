package ir

import (
	"reflect"
	"sort"
	"testing"
)

func TestDiffPairsWatchesWithChangedIDs(t *testing.T) {
	field := func(controller, kind string, path ...string) Watch {
		w := Watch{Controller: controller, Resource: GVK{Group: "demo", Version: "v1", Kind: kind}, Mapping: KeyMapping{Kind: MappingField, FieldPath: path}, Evidence: Extracted}
		w.ID = w.StableID()
		return w
	}
	a, b := field("database", "Instance", "Spec", "Database"), field("database", "Instance", "Spec", "Owner")
	declared := b
	declared.Evidence = Declared
	backup, other, third := field("backup", "Instance", "Spec", "Owner"), field("database", "Other", "Spec", "Owner"), field("database", "Instance", "Spec", "Third")
	for _, tc := range []struct {
		name     string
		old, new []Watch
		want     Delta
	}{
		{"changed field path", []Watch{a}, []Watch{b},
			Delta{Changed: []Change{{Kind: RecordWatch, ID: b.ID, PreviousID: a.ID, Fields: []string{"mapping"}}}}},
		{"changed field path and evidence", []Watch{a}, []Watch{declared},
			Delta{Changed: []Change{{Kind: RecordWatch, ID: b.ID, PreviousID: a.ID, Fields: []string{"mapping", "evidence"}}}}},
		{"other controller is not paired", []Watch{a}, []Watch{backup},
			Delta{Added: []Change{{Kind: RecordWatch, ID: backup.ID}}, Removed: []Change{{Kind: RecordWatch, ID: a.ID}}}},
		{"other resource is not paired", []Watch{a}, []Watch{other},
			Delta{Added: []Change{{Kind: RecordWatch, ID: other.ID}}, Removed: []Change{{Kind: RecordWatch, ID: a.ID}}}},
		{"ambiguous pairing is not paired", []Watch{a}, []Watch{b, third},
			Delta{Added: watchChanges(b.ID, third.ID), Removed: []Change{{Kind: RecordWatch, ID: a.ID}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Diff(Document{Watches: tc.old}, Document{Watches: tc.new})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// watchChanges lists watch changes for ids in ID order, as Diff does.
func watchChanges(ids ...string) []Change {
	sort.Strings(ids)
	out := make([]Change, 0, len(ids))
	for _, id := range ids {
		out = append(out, Change{Kind: RecordWatch, ID: id})
	}
	return out
}

func TestDiffChangedWatchFieldPath(t *testing.T) {
	old, new := sampleDocument(), sampleDocument()
	new.Watches[1].Mapping.FieldPath = []string{"Spec", "Owner"}
	d := Diff(old, new)
	want := Delta{Changed: []Change{{Kind: RecordWatch, ID: "w2", Fields: []string{"mapping"}}}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("got %+v, want %+v", d, want)
	}
}

func TestDiffIdenticalAndMovedRecords(t *testing.T) {
	old, new := sampleDocument(), sampleDocument()
	if d := Diff(old, new); !d.Empty() {
		t.Fatalf("identical documents differ: %+v", d)
	}
	// A record that only moved in its file is not changed.
	new.Watches[0].Provenance.Source.Line = 99
	if d := Diff(old, new); !d.Empty() {
		t.Fatalf("provenance-only change reported: %+v", d)
	}
}

func TestDiffAddedRemovedChanged(t *testing.T) {
	old, new := sampleDocument(), sampleDocument()
	new.Watches = new.Watches[:1]
	new.Constraints = append(new.Constraints, Constraint{ID: "c2", Name: "extra", Expression: "true"})
	new.Effects[0].Operation = OpPatch
	new.Controllers = nil
	d := Diff(old, new)
	want := Delta{
		Added:   []Change{{Kind: RecordConstraint, ID: "c2"}},
		Removed: []Change{{Kind: RecordController, ID: "database"}, {Kind: RecordWatch, ID: "w2"}},
		Changed: []Change{{Kind: RecordEffect, ID: old.Effects[0].ID, Fields: []string{"operation"}}},
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("got %+v, want %+v", d, want)
	}
}
