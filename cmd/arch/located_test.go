package main

import (
	"errors"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/goflow"
)

// located keeps every frontend diagnostic and makes each file relative.
func TestLocatedKeepsEveryDiagnostic(t *testing.T) {
	first := func() *goflow.Diagnostic {
		return &goflow.Diagnostic{File: "/m/a.go", Line: 3, Construct: "go", Reason: "unsupported Go statement"}
	}
	second := func() *goflow.Diagnostic {
		return &goflow.Diagnostic{File: "/m/a.go", Line: 7, Construct: "for", Reason: "unsupported Go statement"}
	}
	plain := errors.New("load failed")
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"one", first(), "a.go:3: unsupported Go statement"},
		{"two", goflow.Diagnostics{first(), second()}, "a.go:3: unsupported Go statement\na.go:7: unsupported Go statement"},
		{"plain", plain, "load failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := located("/m", tc.err)
			if got.Error() != tc.want {
				t.Fatalf("got %q, want %q", got.Error(), tc.want)
			}
			if ds, _ := goflow.AsDiagnostics(got); tc.name == "two" && len(ds) != 2 {
				t.Fatalf("diagnostics %v", ds)
			}
		})
	}
}
