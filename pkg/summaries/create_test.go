package summaries

import (
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

func TestBuiltinCreate(t *testing.T) {
	s, err := Builtin().Lookup(ir.OpCreate, ir.ConsistencyDirect)
	if err != nil {
		t.Fatal(err)
	}
	if !equalNames(outcomeNames(s), "created", "alreadyExists", "error") {
		t.Fatalf("outcomes %v", outcomeNames(s))
	}
	created, exists, failure := s.Outcomes[0], s.Outcomes[1], s.Outcomes[2]
	if created.Guard != "!$exists" || created.Updates["$exists"] != "true" || created.Updates["$object"] != "$value" || created.Error != "" {
		t.Fatalf("created %+v", created)
	}
	if exists.Guard != "$exists" || exists.Error != ErrorAlreadyExists || failure.Error != ErrorTransient {
		t.Fatalf("alreadyExists %+v error %+v", exists, failure)
	}
	// Each guard and update parses and evaluates as CEL after substitution.
	vars := map[string]any{"exists": false, "object": "", "value": "desired", "resourceVersion": int64(2), "snapshotVersion": int64(0)}
	guardsParse(t, s, vars)
	b := bareBindings(s)
	if v := evalTemplate(t, created.Guard, b, vars); v != true {
		t.Fatalf("created guard on an absent object: %v", v)
	}
	if v := evalTemplate(t, exists.Guard, b, vars); v != false {
		t.Fatalf("alreadyExists guard on an absent object: %v", v)
	}
	if v := evalTemplate(t, created.Updates["$resourceVersion"], b, vars); v != int64(3) {
		t.Fatalf("version bump: %v", v)
	}
}
