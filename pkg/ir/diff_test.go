package ir

import (
	"reflect"
	"testing"
)

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
