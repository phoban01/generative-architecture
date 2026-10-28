package report

import (
	"fmt"
	"reflect"

	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/receipt"
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

// Categories lists the finding categories in report order.
var Categories = []string{CategorySafety, CategoryConformance, CategoryUnsupported, CategoryTool}

// Modes of a run (EVID-3). Strict (verification) mode fails on every
// finding. Inventory mode reports unsupported findings and fails on the
// others.
const (
	ModeStrict    = "strict"
	ModeInventory = "inventory"
)

// ModeOf returns the mode a --strict flag selects.
func ModeOf(strict bool) string {
	if strict {
		return ModeStrict
	}
	return ModeInventory
}

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
	// Property names the violated property of a safety finding, when known.
	Property string `yaml:"property,omitempty"`
	Message  string `yaml:"message"`
}

// Sampling records the bounds of a Quint simulation.
type Sampling struct {
	Samples int   `yaml:"samples"`
	Steps   int   `yaml:"steps"`
	Seed    int64 `yaml:"seed"`
}

// Verification is the result of arch check, arch replay and the example
// evidence runs. It embeds the receipt of the inputs with the exact tool
// versions that ran and the abstraction bounds (EVID-5), and states its
// scope: bounded sampling is never exhaustive verification.
type Verification struct {
	// Name is the document's metadata.name.
	Name   string `yaml:"-"`
	Result string `yaml:"result"`
	// Mode is ModeStrict or ModeInventory: how the run treats unsupported
	// findings.
	Mode string `yaml:"mode"`
	// Failures counts the findings of each category. Conclude sets it.
	// Every category appears, with zero when it has no finding.
	Failures    map[string]int    `yaml:"failures"`
	Scope       string            `yaml:"scope"`
	Model       string            `yaml:"model,omitempty"`
	ModelSHA256 string            `yaml:"modelSha256,omitempty"`
	Sampling    *Sampling         `yaml:"sampling,omitempty"`
	Traces      int               `yaml:"traces"`
	Transitions int               `yaml:"transitions"`
	Coverage    map[string]int    `yaml:"coverage,omitempty"`
	TraceHashes map[string]string `yaml:"traceHashes,omitempty"`
	Findings    []Finding         `yaml:"findings"`
	// Warnings lists problems that do not block the run, such as drift
	// of a library input (ADR-0009). They do not change Result, Failures
	// or Blocking.
	Warnings []string `yaml:"warnings,omitempty"`
	// Bindings lists how the inputs of a composed model were bound.
	// Protocols lists the conformance of bound links to their protocol
	// contracts. They are separate sections: a binding is never evidence
	// of protocol conformance (CON-10).
	Bindings  []BindingResult  `yaml:"bindings,omitempty"`
	Protocols []ProtocolResult `yaml:"protocols,omitempty"`
	// Temporal lists the liveness properties with their status (CON-6).
	Temporal []TemporalResult `yaml:"temporal,omitempty"`
	// Receipt is the input closure of the run. A run without one may
	// record its inputs in InputHashes instead.
	Receipt receipt.Receipt `yaml:"receipt,omitempty"`
	// InputHashes maps input files, relative to the report, to their
	// SHA-256: the input record of a run without a receipt.
	InputHashes map[string]string `yaml:"inputHashes,omitempty"`
	// Generated lists the generated inputs, relative to the output
	// directory, with their hashes.
	Generated []receipt.File `yaml:"generated,omitempty"`
	// Observed lists the variables that implementation replay compared.
	Observed []string `yaml:"observed,omitempty"`
	// Mutations is the detection matrix of a mutation suite: for each
	// mutation, what detected it.
	Mutations map[string]map[string]bool `yaml:"mutations,omitempty"`
}

// Conclude sets Failures, Result and the protocol statuses from the
// findings and returns the result. The result does not depend on the
// mode; Blocking does.
func (v *Verification) Conclude() string {
	v.Failures = countFailures(v.Findings)
	v.Result = resultOf(v.Findings)
	v.concludeProtocols()
	return v.Result
}

func countFailures(findings []Finding) map[string]int {
	counts := make(map[string]int, len(Categories))
	for _, c := range Categories {
		counts[c] = 0
	}
	for _, f := range findings {
		counts[f.Category]++
	}
	return counts
}

func resultOf(findings []Finding) string {
	result := Passed
	for _, f := range findings {
		if f.Category != CategoryUnsupported {
			return Failed
		}
		result = Incomplete
	}
	return result
}

// Blocking lists the findings that fail the run in its mode: every
// finding in strict mode, and every finding but the unsupported ones in
// inventory mode. Findings other than unsupported ones come first.
func (v Verification) Blocking() []Finding {
	var out, unsupported []Finding
	for _, f := range v.Findings {
		if f.Category != CategoryUnsupported {
			out = append(out, f)
		} else if v.Mode == ModeStrict {
			unsupported = append(unsupported, f)
		}
	}
	return append(out, unsupported...)
}

// Validate requires a known result, mode and categories, failure counts
// and a result that agree with the findings, and a valid receipt when one
// is embedded.
func (v Verification) Validate() error {
	switch v.Result {
	case Passed, Incomplete, Failed:
	default:
		return fmt.Errorf("verification report: unknown result %q", v.Result)
	}
	switch v.Mode {
	case ModeStrict, ModeInventory:
	default:
		return fmt.Errorf("verification report: unknown mode %q; expected %s or %s", v.Mode, ModeStrict, ModeInventory)
	}
	for _, f := range v.Findings {
		switch f.Category {
		case CategorySafety, CategoryConformance, CategoryUnsupported, CategoryTool:
		default:
			return fmt.Errorf("verification report: finding %q has unknown category %q", f.Message, f.Category)
		}
	}
	for _, w := range v.Warnings {
		if w == "" {
			return fmt.Errorf("verification report: empty warning")
		}
	}
	if want := countFailures(v.Findings); !reflect.DeepEqual(v.Failures, want) {
		return fmt.Errorf("verification report: failure counts %v do not match the findings, which give %v", v.Failures, want)
	}
	if want := resultOf(v.Findings); v.Result != want {
		return fmt.Errorf("verification report: result %s does not match the findings, which give %s", v.Result, want)
	}
	if err := v.validateComposition(); err != nil {
		return err
	}
	if err := v.validateTemporal(); err != nil {
		return err
	}
	if reflect.ValueOf(v.Receipt).IsZero() {
		return nil
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
	if err := v.Validate(); err != nil {
		return v, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}
