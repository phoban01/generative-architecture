package ir

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestProvenanceRoundTrip(t *testing.T) {
	p := Provenance{
		Module:           "example.org/controller",
		ModuleVersion:    "v1.2.3",
		Revision:         "abc123",
		Source:           Source{File: "controller.go", Line: 42, SHA256: "deadbeef"},
		Generator:        "goflow",
		GeneratorVersion: "0.1.0",
		Bounds:           map[string]string{"steps": "60", "samples": "40"},
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	b, err := yaml.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back Provenance
	if err := yaml.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, back) {
		t.Fatalf("round trip changed the record:\n%s\n%+v", b, back)
	}
}

func TestProvenanceValidate(t *testing.T) {
	if err := (Provenance{Source: Source{SHA256: "x"}}).Validate(); err == nil {
		t.Fatal("missing generator accepted")
	}
	if err := (Provenance{Generator: "g"}).Validate(); err == nil {
		t.Fatal("unpinned input accepted")
	}
	if err := (Provenance{Generator: "g", ModuleVersion: "v1"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Provenance{Generator: "g", Source: Source{SHA256: "x", Line: -1}}).Validate(); err == nil {
		t.Fatal("negative line accepted")
	}
}
