package report

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// Binding statuses. A binding is connectivity only: a bound link says that
// one provider was found for an input, never that the two sides follow a
// protocol (CON-10).
const (
	BindingBound      = "bound"
	BindingAmbiguous  = "ambiguous"
	BindingUnresolved = "unresolved"
)

// Protocol statuses.
const (
	// ProtocolConforms: the run checked states and no property of the
	// protocol was violated. Bounded sampling still limits the claim.
	ProtocolConforms = "conforms"
	// ProtocolViolated: a safety finding names a property of the protocol.
	ProtocolViolated = "violated"
	// ProtocolUnchecked: the run checked no state, so it has no evidence
	// about the protocol.
	ProtocolUnchecked = "unchecked"
)

// BindingResult is the binding of one input port. Link is "from -> to" for
// a bound input and the input port otherwise. Candidates lists the
// providers of an ambiguous input.
type BindingResult struct {
	Link       string   `yaml:"link"`
	Status     string   `yaml:"status"`
	Candidates []string `yaml:"candidates,omitempty"`
	Message    string   `yaml:"message,omitempty"`
}

// ProtocolResult is the conformance of one bound link to its protocol
// contract. Properties lists the generated properties that check it;
// Violations lists those a safety finding names.
type ProtocolResult struct {
	Link       string   `yaml:"link"`
	Protocol   string   `yaml:"protocol"`
	Version    string   `yaml:"version"`
	Status     string   `yaml:"status"`
	Properties []string `yaml:"properties"`
	Violations []string `yaml:"violations,omitempty"`
}

// AssumptionResult is one assumption of a boundary component: what the
// composed model takes for granted about an upstream project. A run
// never checks it; the conformance hook says how it could be checked.
type AssumptionResult struct {
	Component   string `yaml:"component"`
	ID          string `yaml:"id"`
	Statement   string `yaml:"statement"`
	Evidence    string `yaml:"evidence"`
	Conformance string `yaml:"conformance"`
	Reference   string `yaml:"reference,omitempty"`
}

// LinkName is the Link of a bound link.
func LinkName(from, to string) string { return from + " -> " + to }

// concludeProtocols sets the status and violations of each protocol from
// the safety findings and the number of checked traces.
func (v *Verification) concludeProtocols() {
	for i := range v.Protocols {
		p := &v.Protocols[i]
		p.Violations = protocolViolations(*p, v.Findings)
		p.Status = protocolStatus(*p, v.Traces)
	}
}

func protocolViolations(p ProtocolResult, findings []Finding) []string {
	var out []string
	for _, f := range findings {
		if f.Category == CategorySafety && f.Property != "" && slices.Contains(p.Properties, f.Property) && !slices.Contains(out, f.Property) {
			out = append(out, f.Property)
		}
	}
	return out
}

func protocolStatus(p ProtocolResult, traces int) string {
	switch {
	case len(p.Violations) > 0:
		return ProtocolViolated
	case traces == 0:
		return ProtocolUnchecked
	default:
		return ProtocolConforms
	}
}

// validateComposition checks the binding and protocol sections: known
// statuses, a protocol only on a bound link, and protocol statuses that
// agree with the findings.
func (v Verification) validateComposition() error {
	bound := map[string]bool{}
	for _, b := range v.Bindings {
		switch b.Status {
		case BindingBound:
			bound[b.Link] = true
		case BindingAmbiguous, BindingUnresolved:
		default:
			return fmt.Errorf("verification report: binding %s has unknown status %q", b.Link, b.Status)
		}
	}
	for _, a := range v.Assumptions {
		if a.Component == "" || a.ID == "" || (a.Evidence != "assumed" && a.Evidence != "declared") {
			return fmt.Errorf("verification report: assumption %s.%s must name its component and id with assumed or declared evidence, got %q", a.Component, a.ID, a.Evidence)
		}
	}
	for _, p := range v.Protocols {
		if !bound[p.Link] {
			return fmt.Errorf("verification report: protocol %s/%s is on link %s, which is not a bound binding", p.Protocol, p.Version, p.Link)
		}
		if len(p.Properties) == 0 {
			return fmt.Errorf("verification report: protocol %s/%s on %s lists no properties", p.Protocol, p.Version, p.Link)
		}
		want := protocolViolations(p, v.Findings)
		if !slices.Equal(p.Violations, want) || p.Status != protocolStatus(p, v.Traces) {
			return fmt.Errorf("verification report: protocol %s/%s on %s: status %s and violations %v do not match the findings", p.Protocol, p.Version, p.Link, p.Status, p.Violations)
		}
	}
	return nil
}

// WriteSections writes the assumption, binding, protocol and warning sections as
// text, each under its own heading. A bound link without a protocol is
// listed under protocols as unchecked, so a binding never reads as
// conformance. It writes nothing when the report has no bindings, no
// protocols and no warnings.
func (v Verification) WriteSections(w io.Writer) {
	v.writeComposition(w)
	if len(v.Warnings) > 0 {
		fmt.Fprintln(w, "warnings (not blocking):")
		for _, warning := range v.Warnings {
			fmt.Fprintf(w, "  %s\n", warning)
		}
	}
}

func (v Verification) writeComposition(w io.Writer) {
	v.writeAssumptions(w)
	if len(v.Bindings) == 0 && len(v.Protocols) == 0 {
		return
	}
	fmt.Fprintln(w, "bindings (connectivity only, not protocol conformance):")
	for _, b := range v.Bindings {
		line := fmt.Sprintf("  %s: %s", b.Link, b.Status)
		if len(b.Candidates) > 0 {
			line += " (candidates: " + strings.Join(b.Candidates, ", ") + ")"
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w, "protocols:")
	covered := map[string]bool{}
	for _, p := range v.Protocols {
		covered[p.Link] = true
		line := fmt.Sprintf("  %s: %s/%s %s", p.Link, p.Protocol, p.Version, p.Status)
		if len(p.Violations) > 0 {
			line += " (" + strings.Join(p.Violations, ", ") + ")"
		}
		fmt.Fprintln(w, line)
	}
	for _, b := range v.Bindings {
		if b.Status == BindingBound && !covered[b.Link] {
			fmt.Fprintf(w, "  %s: no protocol contract; conformance not checked\n", b.Link)
		}
	}
}

// writeAssumptions lists the boundary assumptions the composed model
// rests on. None of them is checked by the run.
func (v Verification) writeAssumptions(w io.Writer) {
	if len(v.Assumptions) == 0 {
		return
	}
	fmt.Fprintln(w, "assumptions (boundary facts; not checked by this run):")
	for _, a := range v.Assumptions {
		line := fmt.Sprintf("  %s.%s: %s (%s; conformance: %s", a.Component, a.ID, a.Statement, a.Evidence, a.Conformance)
		if a.Reference != "" {
			line += " " + a.Reference
		}
		fmt.Fprintln(w, line+")")
	}
}
