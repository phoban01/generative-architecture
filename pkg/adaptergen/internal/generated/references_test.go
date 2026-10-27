package generated

import (
	"context"
	"strings"
	"testing"

	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/adaptergen/internal/fixture"
)

func TestReferencesUpToDate(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	src, err := adaptergen.GenerateReferenceCheck(fixture.Package, a.Spec)
	if err != nil {
		t.Fatal(err)
	}
	assertGenerated(t, "references.go", src)
}

// The generated check accepts an absent instance and one that names the
// declared Database, and fails when the reference points at a Database the
// abstraction does not declare or is missing.
func TestCheckReferences(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		fields map[string]any
		want   string
	}{
		{"declared Database", map[string]any{"spec.databaseName": "db-1"}, ""},
		{"missing Database", map[string]any{"spec.databaseName": "db-2"}, `spec.databaseName "db-2" names no declared Database`},
		{"no reference", map[string]any{"status.endpoint": "x"}, `spec.databaseName "" names no declared Database`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, mapper := newAPI(t)
			if err := CheckReferences(ctx, api, mapper); err != nil {
				t.Fatalf("absent instance: %v", err)
			}
			create(t, api, mapper, fixture.Instance, "demo", "instance-for-db-1", tc.fields)
			err := CheckReferences(ctx, api, mapper)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("CheckReferences = %v, want %q", err, tc.want)
			}
		})
	}
}
