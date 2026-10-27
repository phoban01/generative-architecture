package likec4

import (
	"testing"

	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/ir"
)

// EffectsOf splits subresource operations, keeps the evidence and source,
// and ResourceView draws a schema-only effect as "uses schema of", never
// as a write.
func TestEffectsOfInResourceView(t *testing.T) {
	db := effect.Resource{Group: "demo.example", Version: "v1", Kind: "Database"}
	effects := EffectsOf("ctl", []effect.Descriptor{
		{ID: "publish", Resource: db, Operation: "status.update", Consistency: "direct"},
		{ID: "check", Resource: db, Operation: "derive", Consistency: "schema"},
		{ID: "read", Resource: db, Operation: "get", Consistency: "cached"},
	}, ir.Declared, ir.Source{File: "effects.yaml", Line: 3})
	if e := effects[0]; e.Subresource != "status" || e.Operation != "update" || e.Evidence != ir.Declared || e.Provenance.Source.Line != 3 {
		t.Fatalf("publish %+v", e)
	}
	_, relations, _, err := ResourceView("r", ir.Document{Controllers: []ir.Controller{{ID: "ctl"}}, Effects: effects})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, r := range relations {
		labels[r.Description] = r.Label
	}
	for description, want := range map[string]string{
		"publish: status.update (direct)": "writes",
		"check: derive (schema)":          "uses schema of",
		"read: get (cached)":              "reads",
	} {
		if labels[description] != want {
			t.Errorf("%s: label %q, want %q (%v)", description, labels[description], want, labels)
		}
	}
}
