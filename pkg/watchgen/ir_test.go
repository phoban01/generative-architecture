package watchgen_test

import (
	"strings"
	"testing"

	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/watchgen"
)

func TestIRWatches(t *testing.T) {
	source, resources := fixture(t)
	result, err := watchgen.Extract(source, "DatabaseReconciler", "SetupWithManager", resources)
	if err != nil {
		t.Fatal(err)
	}
	watches := result.IR("database", "wiring.go")
	if len(watches) != 2 {
		t.Fatalf("expected two watches, got %+v", watches)
	}
	primary, secondary := watches[0], watches[1]
	if !primary.Primary || primary.Mapping.Kind != ir.MappingSelf || primary.Resource.Kind != "Database" {
		t.Fatalf("unexpected primary watch %+v", primary)
	}
	if secondary.Primary || secondary.Mapping.Kind != ir.MappingField || strings.Join(secondary.Mapping.FieldPath, ".") != "Spec.DatabaseName" || secondary.Resource.Kind != "DatabaseInstance" {
		t.Fatalf("unexpected secondary watch %+v", secondary)
	}
	for _, w := range watches {
		if w.ID != w.StableID() || len(w.ID) != 16 || w.Evidence != ir.Extracted || w.Provenance.Generator != watchgen.Generator || w.Provenance.Source.Line == 0 || w.Provenance.Source.SHA256 != result.SourceSHA256 {
			t.Fatalf("incomplete watch record %+v", w)
		}
		if err := w.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if primary.ID == secondary.ID {
		t.Fatal("watches share an id")
	}
	// Moving the builder call must not change the IDs.
	moved, err := watchgen.Extract([]byte(strings.Replace(string(source), "func (r *DatabaseReconciler) SetupWithManager", "\n\nfunc (r *DatabaseReconciler) SetupWithManager", 1)), "DatabaseReconciler", "SetupWithManager", resources)
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range moved.IR("database", "wiring.go") {
		if w.ID != watches[i].ID {
			t.Fatalf("source movement changed id %d", i)
		}
		if w.Provenance.Source.Line == watches[i].Provenance.Source.Line {
			t.Fatalf("expected the line of watch %d to move", i)
		}
	}
}
