package report

import (
	"fmt"

	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/receipt"
)

// VerificationKind is the KRM kind of a verification report.
const VerificationKind = "VerificationReport"

// Finding categories (EVID-2). They repeat the failure categories of
// pkg/conformance, which this package cannot import.
const (
	CategorySafety      = "safety"
	CategoryConformance = "conformance"
	CategoryUnsupported = "unsupported"
	CategoryTool        = "tool"
)

// Results of a verification report.
const (
	// Passed: no finding.
	Passed = "passed"
	// Incomplete: only unsupported findings; part of the evidence could not
	// be produced.
	Incomplete = "incomplete"
	// Failed: at least one safety, conformance or tool finding.
	Failed = "failed"
)

// Finding is one categorized problem of a check or replay. Trace, Step and
// Event locate it in a trace when it arose there.
type Finding struct {
	Category string `yaml:"category"`
	Trace    string `yaml:"trace,omitempty"`
	Step     int    `yaml:"step,omitempty"`
	Event    string `yaml:"event,omitempty"`
	Message  string `yaml:"message"`
}

// Sampling records the bounds of a Quint simulation.
type Sampling struct {
	Samples int   `yaml:"samples"`
	Steps   int   `yaml:"steps"`
	Seed    int64 `yaml:"seed"`
}

// Verification is the result of arch check or arch replay. It embeds the
// receipt of the inputs with the exact tool versions that ran and the
// abstraction bounds (EVID-5), and states its scope: bounded sampling is
// never exhaustive verification.
type Verification struct {
	// Name is the document's metadata.name.
	Name        string            `yaml:"-"`
	Result      string            `yaml:"result"`
	Scope       string            `yaml:"scope"`
	Model       string            `yaml:"model"`
	ModelSHA256 string            `yaml:"modelSha256"`
	Sampling    *Sampling         `yaml:"sampling,omitempty"`
	Traces      int               `yaml:"traces"`
	Transitions int               `yaml:"transitions"`
	Coverage    map[string]int    `yaml:"coverage,omitempty"`
	TraceHashes map[string]string `yaml:"traceHashes,omitempty"`
	Findings    []Finding         `yaml:"findings"`
	Receipt     receipt.Receipt   `yaml:"receipt"`
	// Generated lists the generated inputs, relative to the output
	// directory, with their hashes.
	Generated []receipt.File `yaml:"generated,omitempty"`
}

// Conclude sets Result from the findings and returns it.
func (v *Verification) Conclude() string {
	v.Result = Passed
	for _, f := range v.Findings {
		if f.Category != CategoryUnsupported {
			v.Result = Failed
			return v.Result
		}
		v.Result = Incomplete
	}
	return v.Result
}

// Validate requires known categories and results and a valid receipt.
func (v Verification) Validate() error {
	switch v.Result {
	case Passed, Incomplete, Failed:
	default:
		return fmt.Errorf("verification report: unknown result %q", v.Result)
	}
	for _, f := range v.Findings {
		switch f.Category {
		case CategorySafety, CategoryConformance, CategoryUnsupported, CategoryTool:
		default:
			return fmt.Errorf("verification report: finding %q has unknown category %q", f.Message, f.Category)
		}
	}
	return v.Receipt.Validate()
}

// Write validates the report and writes it as a VerificationReport status
// document. An empty finding list is written as [].
func (v Verification) Write(path string) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if v.Findings == nil {
		v.Findings = []Finding{}
	}
	name := v.Name
	if name == "" {
		name = "verification"
	}
	return krm.WriteResult(path, VerificationKind, name, v)
}

// ReadVerification loads a report written by Write.
func ReadVerification(path string) (Verification, error) {
	var v Verification
	if err := krm.ReadResult(path, VerificationKind, &v); err != nil {
		return v, err
	}
	return v, v.Validate()
}
