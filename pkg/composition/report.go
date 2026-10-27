package composition

import "generative-architecture/pkg/report"

// ReportSections turns the links and join diagnostics of a composition and
// its protocol checks (Result.Protocols) into the binding and protocol
// sections of a verification report. Every link is bound. An unresolved or
// ambiguous provider diagnostic is an unbound input. A protocol check lists
// the generated properties that decide its status; report.Conclude sets
// the status from the findings. A link never yields a protocol result on
// its own, so binding is never reported as conformance.
func ReportSections(links []Link, diagnostics []Diagnostic, checks []ProtocolCheck) ([]report.BindingResult, []report.ProtocolResult) {
	var bindings []report.BindingResult
	for _, l := range links {
		bindings = append(bindings, report.BindingResult{Link: report.LinkName(l.From, l.To), Status: report.BindingBound})
	}
	for _, d := range diagnostics {
		status := ""
		switch d.Code {
		case DiagUnresolvedProvider:
			status = report.BindingUnresolved
		case DiagAmbiguousProvider:
			status = report.BindingAmbiguous
		default:
			continue
		}
		bindings = append(bindings, report.BindingResult{Link: d.Port, Status: status, Candidates: append([]string(nil), d.Candidates...), Message: d.Message})
	}
	var protocols []report.ProtocolResult
	for _, c := range checks {
		protocols = append(protocols, report.ProtocolResult{Link: report.LinkName(c.Link.From, c.Link.To), Protocol: c.Protocol, Version: c.Version, Properties: append([]string(nil), c.Properties...)})
	}
	return bindings, protocols
}
