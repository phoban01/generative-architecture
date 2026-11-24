package quint

import (
	"encoding/json"
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
