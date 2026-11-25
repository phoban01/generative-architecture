package quint

import (
	"context"
	"os"
	"strings"

	"encoding/json"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/model"
	"testing"
)

func TestStripRunMetadata(t *testing.T) {
	in := []byte(`{"#meta":{"format":"ITF","format-description":"https://apalache-mc.org/docs/adr/015adr-trace.html","source":"/tmp/architecture-quint-1/Model.qnt","status":"ok","description":"Created by Quint on Wed Oct 28 2026","timestamp":1793217700701},"vars":["b","a"],"states":[{"#meta":{"index":0},"b":true,"a":{"#bigint":"1"}}]}`)
	want := `{"#meta":{"format":"ITF","format-description":"https://apalache-mc.org/docs/adr/015adr-trace.html","status":"ok"},"vars":["b","a"],"states":[{"#meta":{"index":0},"b":true,"a":{"#bigint":"1"}}]}`
	got, err := stripRunMetadata(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("stripRunMetadata =\n%s\nwant\n%s", got, want)
	}
	if !json.Valid(got) {
		t.Error("output is not valid JSON")
	}
	again, err := stripRunMetadata(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != want {
		t.Errorf("stripRunMetadata is not idempotent:\n%s", again)
	}
}

func TestStripRunMetadataWithoutMeta(t *testing.T) {
	in := []byte(`{"vars":["a"],"states":[{"a":1}]}`)
	got, err := stripRunMetadata(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(in) {
		t.Errorf("stripRunMetadata changed a trace without #meta: %s", got)
	}
	if _, err = stripRunMetadata([]byte(`{"#meta":[]}`)); err == nil {
		t.Error("expected an error for a #meta block that is not an object")
	}
	if _, err = stripRunMetadata([]byte(`nope`)); err == nil {
		t.Error("expected an error for a document that is not JSON")
	}
}

// A violated invariant returns the violating trace as the counterexample
// beside the error, so a caller can keep and draw it.
func TestSampleKeepsCounterexample(t *testing.T) {
	exe := os.Getenv("QUINT_BIN")
	if exe == "" {
		t.Skip("QUINT_BIN unset")
	}
	m, err := model.Compile(model.Spec{
		Name:       "Counter",
		State:      map[string]model.Variable{"count": {Type: "int", Initial: "0"}},
		Actions:    []model.Action{{Name: "increment", Guard: "count < 5", Updates: map[string]expr.Expression{"count": "count + 1"}}},
		Properties: []model.Property{{Name: "small", Expression: "count < 3"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Sample(context.Background(), m, Options{Executable: exe, Samples: 2, Steps: 10, Seed: 1})
	if err == nil || !strings.Contains(err.Error(), "[violation]") {
		t.Fatalf("expected a violation, got %v", err)
	}
	c := result.Counterexample
	// Quint may keep states past the first violation; the first state that
	// fails the model's own check is the one the caller reports.
	violating := -1
	for i, s := range c.States {
		if m.Check(s.Values, s.Event) != nil {
			violating = i
			break
		}
	}
	if c.Status != "violation" || violating < 0 || c.States[violating].Values["count"] != int64(3) {
		t.Fatalf("counterexample %+v", c)
	}
	if len(result.CounterexampleITF) == 0 || strings.Contains(string(result.CounterexampleITF), `"source":`) {
		t.Fatalf("counterexample bytes %q", result.CounterexampleITF)
	}
}
