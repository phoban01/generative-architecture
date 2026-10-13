package architecture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func project(t *testing.T) *Project {
	t.Helper()
	p, err := Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func model(t *testing.T) *Model {
	t.Helper()
	m, err := project(t).Compile()
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func TestBindingsDriveBothOutputs(t *testing.T) {
	m := model(t)
	a, _ := m.Action("mac.publish")
	if !strings.Contains(string(a.Locals[0].Value), "claim_leases.get(infra)") || !strings.Contains(m.LikeC4(), "claim -> mac") {
		t.Fatal("binding not reflected in model and diagram")
	}
	infra := m.Project.Contracts["infra"]
	for _, p := range infra.Ports {
		if p.Protocol == "claim-assignment/v1" {
			t.Fatal("InfraMachine must not consume leases")
		}
	}
	for _, a := range m.Actions {
		if a.Owner == "infra" && strings.Contains(a.Guard+fmtUpdates(a.Updates), "claim_leases") {
			t.Fatal("lease state leaked into InfraMachine")
		}
	}
}
func fmtUpdates(m map[string]string) string {
	var s strings.Builder
	for _, v := range m {
		s.WriteString(v)
	}
	return s.String()
}
func TestInvalidContracts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Project)
	}{
		{"missing binding", func(p *Project) { p.System.Bindings = p.System.Bindings[1:] }},
		{"ambiguous binding", func(p *Project) { p.System.Bindings = append(p.System.Bindings, p.System.Bindings[0]) }},
		{"unknown endpoint", func(p *Project) { p.System.Bindings[0].From = "claim.missing" }},
		{"protocol mismatch", func(p *Project) { p.System.Bindings[0].From = "claim.lease" }},
		{"version drift", func(p *Project) { p.System.Components[0].Version = "9.0.0" }},
		{"identity mismatch", func(p *Project) {
			c := p.Contracts["mac"]
			port := c.Ports["lease"]
			port.Key = "namespace/name"
			c.Ports["lease"] = port
		}},
		{"unowned write", func(p *Project) {
			c := p.Contracts["mac"]
			c.Actions[0].Updates["leases"] = Update{Key: "infra", Value: "true"}
		}},
		{"unbound read", func(p *Project) {
			c := p.Contracts["mac"]
			c.Actions[0].Guard = "claim.leases[infra] == claim.leases[infra]"
		}},
		{"wrong value type", func(p *Project) {
			c := p.Contracts["mac"]
			u := c.Actions[0].Updates["claims"]
			u.Value = `"not bool"`
			c.Actions[0].Updates["claims"] = u
		}},
		{"wrong condition type", func(p *Project) {
			c := p.Contracts["mac"]
			u := c.Actions[0].Updates["claims"]
			v := Expression("0")
			u.When = &v
			c.Actions[0].Updates["claims"] = u
		}},
		{"duplicate component", func(p *Project) { p.System.Components = append(p.System.Components, p.System.Components[0]) }},
		{"unknown scenario action", func(p *Project) { p.Scenarios["bad"] = []Step{{Action: "mac.missing", Infra: "infra-reserve"}} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := project(t)
			tc.mutate(p)
			if _, err := p.Compile(); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
func TestDeterministicGenerationAndTopology(t *testing.T) {
	m := model(t)
	again, err := m.Project.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if m.Quint() != again.Quint() || m.LikeC4() != again.LikeC4() {
		t.Fatal("nondeterministic generation")
	}
	m.Project.System.Consumers = append(m.Project.System.Consumers, Consumer{"second", "Second cluster"})
	m.Project.System.InfraMachines = append(m.Project.System.InfraMachines, InfraMachine{"infra-second", "second"})
	changed, err := m.Project.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(changed.Quint(), "infra-second") || !strings.Contains(changed.LikeC4(), "Second cluster") {
		t.Fatal("topology change not propagated")
	}
}
func TestCELSubset(t *testing.T) {
	scope := constants()
	scope["x"] = Symbol{Type: "int", Quint: "x"}
	scope["enabled"] = Symbol{Type: "bool", Quint: "enabled"}
	scope["leases"] = Symbol{Type: "map(string, Assignment)", Quint: "leases"}
	tests := []struct {
		expr                Expression
		expectedType, quint string
	}{
		{"x + 1 < 3 && !enabled", "bool", "(((x + 1) < 3) and not(enabled))"},
		{`{"machineId":"host-1", "epoch":x+1}`, "Assignment", `{ epoch: (x + 1), machineId: "host-1" }`},
		{"enabled ? x : 0", "int", "(if (enabled) x else 0)"},
		{`INFRAS.all(a, INFRAS.all(b, a == b || leases[a].machineId != leases[b].machineId))`, "bool", `INFRAS.forall(a => INFRAS.forall(b => ((a == b) or (leases.get(a).machineId != leases.get(b).machineId))))`},
	}
	for _, tc := range tests {
		t.Run(string(tc.expr), func(t *testing.T) {
			s, err := CompileCEL(tc.expr, scope, tc.expectedType)
			if err != nil {
				t.Fatal(err)
			}
			if s.Quint != tc.quint {
				t.Fatalf("got %s", s.Quint)
			}
		})
	}
	for _, expr := range []Expression{`x.startsWith("a")`, `INFRAS.exists(a,true)`, `has(leases.a)`, `[1,2]`, `1.0`, `1u`, `x && enabled`, `missing == 1`, `INFRAS.all(x,true)`, `{"machineId":"h","epoch":true}`} {
		t.Run("reject "+string(expr), func(t *testing.T) {
			if _, err := CompileCEL(expr, scope, ""); err == nil {
				t.Fatal("unsupported or ill-typed CEL accepted")
			}
		})
	}
	e, err := NewEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.Eval(`{"machineId":"h", "epoch":x+1}`, map[string]any{"x": int64(2)})
	if err != nil || !reflect.DeepEqual(v, map[string]any{"machineId": "h", "epoch": int64(3)}) {
		t.Fatalf("CEL evaluation %v %v", v, err)
	}
	if _, err = e.Eval("missing == 1", nil); err == nil {
		t.Fatal("CEL error swallowed")
	}
}
func TestYAMLValidation(t *testing.T) {
	for _, source := range []string{"guard: true", "guard: x\nguard: y", "guard: x\nunknown: x", "guard: &a x\naccepted: *a", "guard: x\n---\nguard: y"} {
		t.Run(source, func(t *testing.T) {
			var out struct {
				Guard    Expression `yaml:"guard"`
				Accepted Expression `yaml:"accepted"`
			}
			if err := DecodeYAML([]byte(source), &out); err == nil {
				t.Fatal("invalid YAML accepted")
			}
		})
	}
}
func TestITFRejectsInvalidValues(t *testing.T) {
	for _, v := range []any{map[string]any{"#bigint": "9223372036854775808"}, map[string]any{"#map": []any{[]any{"a", true}, []any{"a", false}}}, map[string]any{"#set": []any{1}}} {
		if _, err := DecodeITF(v); err == nil {
			t.Fatal("invalid ITF accepted")
		}
	}
	v, err := DecodeITF(map[string]any{"#bigint": "9007199254740993"})
	if err != nil || v != int64(9007199254740993) {
		t.Fatal("int64 precision lost")
	}
	_, err = DecodeITF(json.Number("1.5"))
	if err == nil {
		t.Fatal("fractional ITF integer accepted")
	}
}
func TestRecordedTracesAndMutation(t *testing.T) {
	m := model(t)
	e, err := NewEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"staleLease", "staleEpoch", "transferFromReserve"} {
		trace, err := ReadTrace(filepath.Join(m.Project.Root, "traces", name+".itf.json"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.ValidateCEL(trace, e); err != nil {
			t.Fatal(err)
		}
		good, err := m.Replay(trace, false)
		if err != nil || !good.OK {
			t.Fatalf("conformance failed: %v", err)
		}
		if name != "transferFromReserve" {
			bad, err := m.Replay(trace, true)
			if err != nil || bad.OK || bad.Action != "mac.publish" || bad.ExpectedAccepted || !bad.ActualAccepted {
				t.Fatalf("mutation not detected: %+v %v", bad, err)
			}
		}
	}
	race, err := ReadTrace(filepath.Join(m.Project.Root, "reports/revocation-race.itf.json"))
	if err != nil {
		t.Fatal(err)
	}
	violations, err := m.ValidateCEL(race, e)
	if err != nil || violations["provisionRequiresCurrentLease"] != 1 {
		t.Fatalf("expected counterexample: %v %v", violations, err)
	}
}
func TestContractLockDrift(t *testing.T) {
	m := model(t)
	dir := t.TempDir()
	m.Project.Root = dir
	if err := WriteResource(filepath.Join(dir, "contracts.lock.yaml"), "ContractLock", "contracts", m.Project.Lock); err != nil {
		t.Fatal(err)
	}
	entry := m.Project.Lock["mac"]
	entry.SHA256 = "changed"
	m.Project.Lock["mac"] = entry
	if err := m.Generate(false); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("expected drift error: %v", err)
	}
}
func TestReplayRejectsStaleEvidence(t *testing.T) {
	m := model(t)
	dir := t.TempDir()
	for _, name := range []string{"system.yaml", "scenarios.yaml"} {
		b, err := os.ReadFile(filepath.Join(m.Project.Root, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	m.Project.Root = dir
	if err := os.MkdirAll(filepath.Join(dir, "traces"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteResource(filepath.Join(dir, "contracts.lock.yaml"), "ContractLock", "contracts", m.Project.Lock); err != nil {
		t.Fatal(err)
	}
	if err := WriteResult(filepath.Join(dir, "traces/manifest.yaml"), "TraceManifest", "system-traces", Evidence{"old-model", map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Check(CheckOptions{Replay: true}); err == nil || !strings.Contains(err.Error(), "different model") {
		t.Fatalf("expected stale evidence failure: %v", err)
	}
}
