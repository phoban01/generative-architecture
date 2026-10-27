package composition_test

import (
	"strings"
	"testing"

	"generative-architecture/pkg/composition"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/protocol"
	"generative-architecture/pkg/report"
)

// A composed link whose consumer writes out of order is bound and violates
// its protocol. The report keeps the two apart.
func TestReportSectionsKeepBindingApartFromProtocol(t *testing.T) {
	result, err := composition.ComposeWithProtocols("Joined", handshakeComponents(), []composition.Link{handshakeLink}, nil, map[string]protocol.Contract{"handshake": handshake()})
	if err != nil {
		t.Fatal(err)
	}
	m, err := model.Compile(result.Model)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	if state, err = m.Next(state, "user_consume"); err != nil {
		t.Fatal(err)
	}
	const conforms = "producer_output__user_input_conforms"
	violation := m.Check(state, "user_consume")
	if violation == nil || !strings.Contains(violation.Error(), conforms) {
		t.Fatalf("check: %v", violation)
	}
	diagnostics := []composition.Diagnostic{{Code: composition.DiagAmbiguousProvider, Port: "auditor.input", Message: "two providers", Candidates: []string{"a.out", "b.out"}}, {Code: composition.DiagDuplicatePort, Port: "x.y"}}
	bindings, protocols := composition.ReportSections([]composition.Link{handshakeLink}, diagnostics, result.Protocols)
	if len(bindings) != 2 || bindings[0].Link != "producer.output -> user.input" || bindings[0].Status != report.BindingBound || bindings[1].Status != report.BindingAmbiguous || len(bindings[1].Candidates) != 2 {
		t.Fatalf("bindings %+v", bindings)
	}
	if len(protocols) != 1 || protocols[0].Protocol != "Handshake" || protocols[0].Version != "v1" || len(protocols[0].Properties) != 2 {
		t.Fatalf("protocols %+v", protocols)
	}
	v := report.Verification{Mode: report.ModeStrict, Scope: "test", Traces: 1, Bindings: bindings, Protocols: protocols,
		Findings: []report.Finding{{Category: report.CategorySafety, Step: 1, Event: "user_consume", Property: conforms, Message: violation.Error()}}}
	v.Conclude()
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	if v.Protocols[0].Status != report.ProtocolViolated || v.Bindings[0].Status != report.BindingBound {
		t.Fatalf("binding %+v protocol %+v", v.Bindings[0], v.Protocols[0])
	}
	// Without a protocol the link is still bound, and no protocol result
	// appears.
	bindings, protocols = composition.ReportSections([]composition.Link{{From: "producer.output", To: "user.input"}}, nil, nil)
	if len(bindings) != 1 || len(protocols) != 0 {
		t.Fatalf("bindings %+v protocols %+v", bindings, protocols)
	}
}
