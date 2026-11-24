package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// The summary of a composed run prints the binding and protocol sections
// apart, after the counts line.
func TestSummaryPrintsBindingAndProtocolSections(t *testing.T) {
	v := report.Verification{Mode: report.ModeStrict, Traces: 2,
		Bindings:  []report.BindingResult{{Link: "a.out -> b.in", Status: report.BindingBound}},
		Protocols: []report.ProtocolResult{{Link: "a.out -> b.in", Protocol: "Handshake", Version: "v1", Properties: []string{"a_out__b_in_conforms"}}},
		Findings:  []report.Finding{{Category: report.CategorySafety, Property: "a_out__b_in_conforms", Message: "property a_out__b_in_conforms violated"}},
	}
	v.Conclude()
	var b strings.Builder
	summary(&b, "check", v)
	want := "check failed (strict mode): 2 traces, 0 transitions, 1 findings, 1 safety\n" +
		"bindings (connectivity only, not protocol conformance):\n" +
		"  a.out -> b.in: bound\n" +
		"protocols:\n" +
		"  a.out -> b.in: Handshake/v1 violated (a_out__b_in_conforms)\n"
	if b.String() != want {
		t.Fatalf("summary:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestViolatedProperty(t *testing.T) {
	violation := &model.PropertyViolation{Property: "boundedRetries", Event: "retry"}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{violation, "boundedRetries"},
		{fmt.Errorf("step 3: %w", violation), "boundedRetries"},
		{errors.New("property boundedRetries violated"), ""},
		{errors.New("evaluate guard: no such field"), ""},
	} {
		if got := violatedProperty(tc.err); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.err, got, tc.want)
		}
	}
}
