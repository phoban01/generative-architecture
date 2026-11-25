package conformance_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/conformance"
)

func TestWriteITFRoundTrip(t *testing.T) {
	in := conformance.Trace{Status: "violation", States: []conformance.State{
		{Event: "init", Values: map[string]any{"count": int64(0), "name": "a", "ready": false}},
		{Event: "increment", Values: map[string]any{"count": int64(1), "name": "b", "ready": true}},
	}}
	b, err := conformance.WriteITF(in)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "trace.itf.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := conformance.ReadITF(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip\n got %+v\nwant %+v", out, in)
	}
	if _, err := conformance.WriteITF(conformance.Trace{Status: "ok"}); err == nil {
		t.Fatal("a trace without states was written")
	}
	if _, err := conformance.WriteITF(conformance.Trace{Status: "ok", States: []conformance.State{{Event: "init", Values: map[string]any{"x": 1.5}}}}); err == nil {
		t.Fatal("a float was written")
	}
}
