package report

import (
	"path/filepath"
	"testing"

	"generative-architecture/pkg/krm"
)

func TestSupportWriteReadOrdering(t *testing.T) {
	var s Support
	add := func(bucket string, file string, line int, construct string) {
		t.Helper()
		if err := s.Add(bucket, Item{Kind: "statement", Construct: construct, Reason: "r", Location: Location{File: file, Line: line}}); err != nil {
			t.Fatal(err)
		}
	}
	add(Unsupported, "b.go", 10, "for")
	add(Unsupported, "a.go", 20, "select")
	add(Unsupported, "a.go", 5, "z-go")
	add(Unsupported, "a.go", 5, "a-defer")
	add(Included, "a.go", 1, "if")
	add(Summarized, "a.go", 2, "client.Get")
	if err := s.Add("maybe", Item{}); err == nil {
		t.Fatal("expected error for unknown bucket")
	}

	path := filepath.Join(t.TempDir(), "support.yaml")
	if err := s.Write(path); err != nil {
		t.Fatal(err)
	}
	var back Support
	if err := krm.ReadResult(path, Kind, &back); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(back.Unsupported))
	for _, it := range back.Unsupported {
		got = append(got, it.Location.File+":"+itoa(it.Location.Line)+":"+it.Construct)
	}
	want := []string{"a.go:5:a-defer", "a.go:5:z-go", "a.go:20:select", "b.go:10:for"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: got %v, want %v", i, got, want)
		}
	}
	if len(back.Included) != 1 || len(back.Summarized) != 1 || back.Summarized[0].Construct != "client.Get" {
		t.Fatalf("unexpected buckets: %+v", back)
	}
	r, err := Read(path)
	if err != nil || len(r.Unsupported) != 4 {
		t.Fatalf("Read: %v %+v", err, r)
	}
}

func TestEmptyReportHasExplicitBuckets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "support.yaml")
	if err := (&Support{}).Write(path); err != nil {
		t.Fatal(err)
	}
	r, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Included == nil || r.Summarized == nil || r.Unsupported == nil {
		t.Fatalf("buckets should round-trip as empty lists: %+v", r)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
