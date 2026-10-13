package controllerdemo

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"generative-architecture/examples/machinevendor/architecture"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func setup(t *testing.T, mutation bool) (*architecture.Model, *Adapter) {
	t.Helper()
	p, err := architecture.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	m, err := p.Compile()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(p.System, mutation)
	if err != nil {
		t.Fatal(err)
	}
	return m, a
}
func must(t *testing.T, accepted bool, err error) {
	t.Helper()
	if err != nil || !accepted {
		t.Fatalf("operation failed: accepted=%v err=%v", accepted, err)
	}
}
func TestAllRecordedControllerTraces(t *testing.T) {
	m, _ := setup(t, false)
	paths, err := filepath.Glob(filepath.Join(m.Project.Root, "traces/*.itf.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 204 {
		t.Fatalf("unexpected trace count %d", len(paths))
	}
	paths = append(paths, filepath.Join(m.Project.Root, "reports/revocation-race.itf.json"))
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			trace, err := architecture.ReadTrace(path)
			if err != nil {
				t.Fatal(err)
			}
			a, err := New(m.Project.System, false)
			if err != nil {
				t.Fatal(err)
			}
			result, err := m.ReplayAdapter(trace, a)
			if err != nil || !result.OK {
				t.Fatalf("controller replay: %+v, %v", result, err)
			}
		})
	}
}
func TestControllerMutationDetected(t *testing.T) {
	for _, name := range []string{"staleLease", "staleEpoch"} {
		t.Run(name, func(t *testing.T) {
			m, a := setup(t, true)
			trace, err := architecture.ReadTrace(filepath.Join(m.Project.Root, "traces", name+".itf.json"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := m.ReplayAdapter(trace, a)
			if err != nil || result.OK || result.Action != "mac.publish" || result.ExpectedAccepted || !result.ActualAccepted {
				t.Fatalf("mutation not detected: %+v, %v", result, err)
			}
		})
	}
}
func TestWholeReconcileLifecycleAndIdempotence(t *testing.T) {
	_, a := setup(t, false)
	id := "infra-customer"
	ok, err := a.InfraRequest(id)
	must(t, ok, err)
	req := reconcile.Request{NamespacedName: key(id)}
	run := func() {
		t.Helper()
		if _, err := a.mac.Reconcile(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	run()
	claim, err := get(a.ctx, a.api, claimGVK, key(id))
	if err != nil {
		t.Fatal(err)
	}
	if len(claim.GetOwnerReferences()) != 1 || claim.GetOwnerReferences()[0].UID != "demo-infra-customer" {
		t.Fatal("claim ownership missing")
	}
	ok, err = a.ClaimGrant(id, "host-1")
	must(t, ok, err)
	run()
	infra, err := get(a.ctx, a.api, infraGVK, key(id))
	if err != nil {
		t.Fatal(err)
	}
	if str(infra, "status", "machineId") != "host-1" {
		t.Fatal("Reconcile did not publish")
	}
	before, err := a.Project()
	if err != nil {
		t.Fatal(err)
	}
	run()
	after, err := a.Project()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("reconcile not semantically idempotent")
	}
	ok, err = a.ClaimRevoke(id)
	must(t, ok, err)
	run()
	infra, err = get(a.ctx, a.api, infraGVK, key(id))
	if err != nil {
		t.Fatal(err)
	}
	if str(infra, "status", "machineId") != "" {
		t.Fatal("Reconcile did not clear")
	}
}
func TestGeneratedDispatchRejectsMalformedActions(t *testing.T) {
	_, a := setup(t, false)
	for _, tc := range []struct {
		action string
		args   map[string]string
	}{
		{"mac.observe", nil},
		{"mac.observe", map[string]string{"infra": "unknown"}},
		{"mac.observe", map[string]string{"host": "host-1"}},
		{"mac.observe", map[string]string{"infra": "infra-customer", "extra": "x"}},
		{"unknown.action", map[string]string{}},
	} {
		if _, err := a.Apply(tc.action, tc.args); err == nil {
			t.Fatal("invalid dispatch accepted", tc)
		}
	}
}
