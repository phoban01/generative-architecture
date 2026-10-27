package report

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// composedVerification has two bound links, one of them under a protocol
// whose generated property a safety finding names.
func composedVerification() Verification {
	v := sampleVerification()
	v.Traces = 3
	v.Bindings = []BindingResult{
		{Link: LinkName("producer.output", "user.input"), Status: BindingBound},
		{Link: LinkName("producer.output", "auditor.input"), Status: BindingBound},
	}
	v.Protocols = []ProtocolResult{{Link: LinkName("producer.output", "user.input"), Protocol: "Handshake", Version: "v1", Properties: []string{"p_conforms", "p_doneMeansConsumed"}}}
	v.Findings = []Finding{{Category: CategorySafety, Trace: "trace-001.itf.json", Step: 2, Event: "user_consume", Property: "p_conforms", Message: "property p_conforms violated"}}
	return v
}

func TestProtocolSectionRoundTrip(t *testing.T) {
	v := composedVerification()
	if v.Conclude() != Failed {
		t.Fatalf("result %s", v.Result)
	}
	if p := v.Protocols[0]; p.Status != ProtocolViolated || !reflect.DeepEqual(p.Violations, []string{"p_conforms"}) {
		t.Fatalf("protocol %+v", p)
	}
	path := filepath.Join(t.TempDir(), "report.yaml")
	if err := v.Write(path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadVerification(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Bindings, v.Bindings) || !reflect.DeepEqual(back.Protocols, v.Protocols) || back.Findings[0].Property != "p_conforms" {
		t.Fatalf("round trip bindings %+v protocols %+v", back.Bindings, back.Protocols)
	}
}

// The binding and the protocol violation appear in separate sections. The
// link without a protocol is bound, and its binding does not read as
// conformance.
func TestProtocolSectionsRenderSeparately(t *testing.T) {
	v := composedVerification()
	v.Conclude()
	var b strings.Builder
	v.WriteSections(&b)
	out := b.String()
	bindings, protocols, ok := strings.Cut(out, "protocols:\n")
	if !ok || !strings.HasPrefix(bindings, "bindings (connectivity only, not protocol conformance):\n") {
		t.Fatalf("sections:\n%s", out)
	}
	for _, want := range []string{"  producer.output -> user.input: bound\n", "  producer.output -> auditor.input: bound\n"} {
		if !strings.Contains(bindings, want) {
			t.Errorf("bindings section lacks %q:\n%s", want, bindings)
		}
	}
	if strings.Contains(bindings, "Handshake") || strings.Contains(bindings, "violated") {
		t.Errorf("bindings section mentions the protocol:\n%s", bindings)
	}
	for _, want := range []string{"  producer.output -> user.input: Handshake/v1 violated (p_conforms)\n", "  producer.output -> auditor.input: no protocol contract; conformance not checked\n"} {
		if !strings.Contains(protocols, want) {
			t.Errorf("protocols section lacks %q:\n%s", want, protocols)
		}
	}
	if strings.Contains(protocols, ": bound") {
		t.Errorf("protocols section shows a binding:\n%s", protocols)
	}
	var empty strings.Builder
	sampleVerification().WriteSections(&empty)
	if empty.Len() != 0 {
		t.Fatalf("report without composition wrote %q", empty.String())
	}
}

func TestProtocolStatus(t *testing.T) {
	v := composedVerification()
	v.Findings = nil
	v.Conclude()
	if v.Protocols[0].Status != ProtocolConforms {
		t.Fatalf("status %s", v.Protocols[0].Status)
	}
	v.Traces = 0
	v.Conclude()
	if v.Protocols[0].Status != ProtocolUnchecked {
		t.Fatalf("status without traces %s", v.Protocols[0].Status)
	}
}

func TestProtocolSectionValidation(t *testing.T) {
	for name, edit := range map[string]func(*Verification){
		"status disagrees with findings": func(v *Verification) { v.Protocols[0].Status = ProtocolConforms },
		"protocol on unbound link":       func(v *Verification) { v.Bindings[0].Status = BindingUnresolved },
		"protocol without binding":       func(v *Verification) { v.Bindings = nil },
		"unknown binding status":         func(v *Verification) { v.Bindings[1].Status = "linked" },
		"protocol without properties":    func(v *Verification) { v.Protocols[0].Properties = nil },
	} {
		t.Run(name, func(t *testing.T) {
			v := composedVerification()
			v.Conclude()
			edit(&v)
			if err := v.Validate(); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
