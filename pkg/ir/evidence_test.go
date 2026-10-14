package ir

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestEvidenceValidate(t *testing.T) {
	for _, e := range []Evidence{Extracted, Observed, Declared, Assumed, Unresolved} {
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []Evidence{"", "Extracted", "guessed"} {
		if err := e.Validate(); err == nil {
			t.Fatalf("%q accepted", e)
		}
	}
}

func TestStrongest(t *testing.T) {
	order := []Evidence{Extracted, Observed, Declared, Assumed, Unresolved}
	for i, a := range order {
		for _, b := range order[i:] {
			if Strongest(a, b) != a || Strongest(b, a) != a {
				t.Fatalf("Strongest(%s, %s) != %s", a, b, a)
			}
		}
	}
	if Strongest("bogus", Unresolved) != Unresolved {
		t.Fatal("invalid evidence outranked unresolved")
	}
}

func TestEvidenceYAML(t *testing.T) {
	b, err := yaml.Marshal(struct {
		E Evidence `yaml:"evidence"`
	}{Observed})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "evidence: observed\n" {
		t.Fatalf("unexpected YAML %q", b)
	}
	var back struct {
		E Evidence `yaml:"evidence"`
	}
	if err := yaml.Unmarshal(b, &back); err != nil || back.E != Observed {
		t.Fatalf("round trip: %v %q", err, back.E)
	}
}
