package composition_test

import (
	"generative-architecture/pkg/composition"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	"os"
	"path/filepath"
	"testing"
)

func components() []composition.Component {
	source := model.Spec{Name: "Producer", State: map[string]model.Variable{"value": {Type: "string", Initial: `"old"`}}, Actions: []model.Action{{Name: "publish", Guard: "true", Updates: map[string]expr.Expression{"value": `"value"`}}}, Properties: []model.Property{{Name: "initValue", Expression: `event != "init" || value == "old"`}}}
	sink := model.Spec{Name: "Consumer", State: map[string]model.Variable{"input": {Type: "string", Initial: `"old"`}, "snapshot": {Type: "string", Initial: `""`}}, Actions: []model.Action{{Name: "read", Guard: "true", Updates: map[string]expr.Expression{"snapshot": "input"}, Effect: "get"}}, Effects: []effect.Descriptor{{ID: "get", Resource: effect.Resource{Version: "v1", Kind: "ConfigMap"}, Operation: "get", Consistency: "cached"}}}
	return []composition.Component{{Name: "producer", Model: source, Ports: map[string]composition.Port{"output": {State: "value", Contract: "data", Version: "v1", Direction: "out"}}}, {Name: "consumer", Model: sink, Ports: map[string]composition.Port{"input": {State: "input", Contract: "data", Version: "v1", Direction: "in"}}}}
}
func TestSharedPortAndPrivateSnapshot(t *testing.T) {
	c := components()
	result, err := composition.Compose("Joined", c, []composition.Link{{From: "producer.output", To: "consumer.input"}}, []model.Property{{Name: "shared", Expression: "producer.value == consumer.input"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := model.Compile(result.Model)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := m.Initial()
	if err = m.Check(state, "init"); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"consumer_read", "producer_publish"} {
		state, err = m.Next(state, event)
		if err != nil {
			t.Fatal(err)
		}
		if err = m.Check(state, event); err != nil {
			t.Fatal(err)
		}
	}
	if state["consumer_snapshot"] != "old" || state["producer_value"] != "value" {
		t.Fatalf("shared state or literal substitution wrong: %v", state)
	}
	d, err := m.Effects().LookupFor("consumer", effect.Resource{Version: "v1", Kind: "ConfigMap"}, "get", "cached")
	if err != nil || d.ID != "consumer_get" {
		t.Fatalf("actor binding: %v %v", d, err)
	}
	if _, err = m.Effects().Lookup(effect.Resource{Version: "v1", Kind: "ConfigMap"}, "get", "cached"); err == nil {
		t.Fatal("missing actor was accepted")
	}
	c[1].Model.Actions[0].Updates["input"] = `"unauthorized"`
	if _, err = composition.Compose("Joined", c, []composition.Link{{From: "producer.output", To: "consumer.input"}}, nil); err == nil {
		t.Fatal("read-only write accepted")
	}
	c = components()
	p := c[1].Ports["input"]
	p.Version = "v2"
	c[1].Ports["input"] = p
	if _, err = composition.Compose("Joined", c, []composition.Link{{From: "producer.output", To: "consumer.input"}}, nil); err == nil {
		t.Fatal("contract version mismatch accepted")
	}
	if _, err = composition.Compose("Joined", components(), nil, nil); err == nil {
		t.Fatal("unbound port accepted")
	}
}
func TestLocalProjectReceipts(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.org/producer\n\ngo 1.25\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := components()
	if err := krm.Write(filepath.Join(project, "model.yaml"), "TransitionModel", "producer", c[0].Model); err != nil {
		t.Fatal(err)
	}
	if err := krm.Write(filepath.Join(root, "sink.yaml"), "TransitionModel", "consumer", c[1].Model); err != nil {
		t.Fatal(err)
	}
	doc := composition.Document{Name: "AcrossProjects", Projects: map[string]composition.Project{"upstream": {Root: "project", Module: "example.org/producer"}}, Components: []composition.ComponentRef{{Name: "producer", Project: "upstream", Model: "model.yaml", Ports: c[0].Ports}, {Name: "consumer", Model: "sink.yaml", Ports: c[1].Ports}}, Links: []composition.Link{{From: "producer.output", To: "consumer.input"}}}
	write := func() {
		t.Helper()
		if err := krm.Write(filepath.Join(root, "system.yaml"), "SystemComposition", "joined", doc); err != nil {
			t.Fatal(err)
		}
	}
	write()
	before, err := composition.Load(root, "system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Inputs) != 4 {
		t.Fatalf("missing receipts: %v", before.Inputs)
	}
	c[0].Model.Actions[0].Updates["value"] = `"changed"`
	if err := krm.Write(filepath.Join(project, "model.yaml"), "TransitionModel", "producer", c[0].Model); err != nil {
		t.Fatal(err)
	}
	after, err := composition.Load(root, "system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(project, "model.yaml")
	if before.Inputs[key] == after.Inputs[key] {
		t.Fatal("drift unrecorded")
	}
	doc.Components[0].Model = "../sink.yaml"
	write()
	if _, err = composition.Load(root, "system.yaml"); err == nil {
		t.Fatal("project escape accepted")
	}
}

func TestAutomaticContractJoinsRejectAmbiguity(t *testing.T) {
	c := components()
	links, err := composition.AutoLink(c, nil)
	if err != nil || len(links) != 1 || links[0].From != "producer.output" {
		t.Fatalf("%v %v", links, err)
	}
	clone := c[0]
	clone.Name = "another"
	c = append(c, clone)
	if _, err := composition.AutoLink(c, nil); err == nil {
		t.Fatal("guessed ambiguous provider")
	}
	if _, err := composition.AutoLink(c, links); err != nil {
		t.Fatal(err)
	}
}
