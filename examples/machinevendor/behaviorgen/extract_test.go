package behaviorgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) ([]byte, Config) {
	t.Helper()
	root := "../../.."
	cfg, err := loadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, cfg.Source))
	if err != nil {
		t.Fatal(err)
	}
	return b, cfg
}
func TestActualGoBranchAndSourceMutation(t *testing.T) {
	source, cfg := fixture(t)
	original, err := Extract(source, cfg)
	if err != nil {
		t.Fatal(err)
	}
	mutantSource, err := RemoveStaleCheck(source, cfg)
	if err != nil {
		t.Fatal(err)
	}
	mutant, err := Extract(mutantSource, cfg)
	if err != nil {
		t.Fatal(err)
	}
	q, err := original.Quint()
	if err != nil {
		t.Fatal(err)
	}
	mq, err := mutant.Quint()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q, "observed != claim") || strings.Contains(mq, "observed != claim") {
		t.Fatal("source mutation did not change extracted behavior")
	}
	for _, property := range cfg.Requirements {
		line := " val " + property.Name + " = "
		propertyLine := func(text string) string {
			for _, s := range strings.Split(text, "\n") {
				if strings.HasPrefix(s, line) {
					return s
				}
			}
			return ""
		}
		if propertyLine(q) == "" || propertyLine(q) != propertyLine(mq) {
			t.Fatal("requirement changed with implementation")
		}
	}
	if original.Nodes[original.Entry].Kind != "readClaim" {
		t.Fatal("entry must preserve initial API read")
	}
	var kinds []string
	for _, n := range original.Nodes {
		if n.Kind == "readClaim" || n.Kind == "updateStatus" {
			kinds = append(kinds, n.Kind)
		}
	}
	if len(kinds) != 2 {
		t.Fatal("API read and commit must be distinct transitions")
	}
	if original.MethodSHA256 == mutant.MethodSHA256 {
		t.Fatal("missing source provenance")
	}
}
func TestWriteExpressionIsExtracted(t *testing.T) {
	source, cfg := fixture(t)
	changed := strings.Replace(string(source), `SetNestedField(infra.Object, observed.MachineID, "status", "machineId")`, `SetNestedField(infra.Object, "wrong-machine", "status", "machineId")`, 1)
	p, err := Extract([]byte(changed), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range p.Nodes {
		if n.Kind == "setField" {
			if n.Expression != `"wrong-machine"` {
				t.Fatal("write expression was hardcoded")
			}
			return
		}
	}
	t.Fatal("write not extracted")
}
func TestUnknownCodeFailsClosed(t *testing.T) {
	source, cfg := fixture(t)
	for _, tc := range []struct{ name, old, new, want string }{
		{"unknown call", `err = r.Client.Status().Update(ctx, infra)`, `doSomethingUnknown(); err = r.Client.Status().Update(ctx, infra)`, "unsupported Go statement"},
		{"unknown condition", `observed.MachineID == "" ||`, `arbitrary(observed) ||`, "unsupported Go expression"},
		{"changed helper", `epoch, _, _ := unstructured.NestedInt64(o.Object, "status", "epoch")`, `epoch, _, _ := unstructured.NestedInt64(o.Object, "status", "different")`, "summary needs review"},
		{"unknown field write", `"status", "machineId"); err != nil`, `"status", "different"); err != nil`, "unsupported Go statement"},
		{"loop", `err = r.Client.Status().Update(ctx, infra)`, `for { }; err = r.Client.Status().Update(ctx, infra)`, "unsupported Go statement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(string(source), tc.old, tc.new, 1)
			if changed == string(source) {
				t.Fatal("test mutation did not apply")
			}
			_, err := Extract([]byte(changed), cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %s, got %v", tc.want, err)
			}
		})
	}
}
