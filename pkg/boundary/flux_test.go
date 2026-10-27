package boundary_test

import (
	"generative-architecture/pkg/composition"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/model"
	"strings"
	"testing"
)

func TestFluxKustomizationBoundary(t *testing.T) {
	b := loadLibrary(t, "flux/kustomization.yaml")
	if b.Spec.Upstream != "github.com/fluxcd/kustomize-controller" || b.Spec.Version != "v1.4.0" {
		t.Fatalf("pin %s@%s", b.Spec.Upstream, b.Spec.Version)
	}
	for _, tc := range []struct {
		id       string
		evidence ir.Evidence
		mentions string
	}{
		{id: "interval", evidence: ir.Declared, mentions: "spec.interval"},
		{id: "serverSideApply", evidence: ir.Declared, mentions: "field manager kustomize-controller"},
		{id: "dependsOn", evidence: ir.Declared, mentions: "spec.dependsOn"},
		{id: "prune", evidence: ir.Declared, mentions: "spec.prune"},
		{id: "readyAfterApply", evidence: ir.Assumed, mentions: "Ready"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			for _, a := range b.Spec.Assumptions {
				if a.ID != tc.id {
					continue
				}
				if a.Evidence != tc.evidence || !strings.Contains(a.Statement, tc.mentions) {
					t.Fatalf("assumption %+v", a)
				}
				return
			}
			t.Fatalf("no assumption %s", tc.id)
		})
	}
}

// kustomizationConsumer is an owned controller that acts on the objects a
// Kustomization applies once the Kustomization is ready.
func kustomizationConsumer() (model.Spec, map[string]composition.Port) {
	spec := model.Spec{Name: "Consumer", State: map[string]model.Variable{
		"manifests":          {Type: "bool", Initial: "false"},
		"kustomizationReady": {Type: "bool", Initial: "false"},
		"reconciled":         {Type: "bool", Initial: "false"},
	}, Actions: []model.Action{{Name: "reconcile", Guard: "manifests && kustomizationReady && !reconciled", Updates: map[string]expr.Expression{"reconciled": "true"}}}}
	ports := map[string]composition.Port{
		"manifests":     {State: "manifests", Contract: "kustomize.toolkit.fluxcd.io/applied", Version: "v1", Direction: "in"},
		"kustomization": {State: "kustomizationReady", Contract: composition.ResourceContract(ir.GVK{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "Kustomization"}), Version: composition.ResourceContractVersion, Direction: "in"},
	}
	return spec, ports
}

func TestFluxKustomizationComposes(t *testing.T) {
	consumer, ports := kustomizationConsumer()
	loaded, err := system{boundaries: map[string]string{"flux": "flux/kustomization.yaml"}, consumer: consumer, ports: ports}.compose(t)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Components[0].Evidence != ir.Assumed {
		t.Fatalf("flux component evidence %q", loaded.Components[0].Evidence)
	}
	for _, tc := range []struct {
		name   string
		events []string
		err    string
		want   map[string]any
	}{
		{name: "consumer acts after apply and ready", events: []string{"flux_dependenciesReady", "flux_apply", "flux_markReady", "consumer_reconcile"}, want: map[string]any{"consumer_reconciled": true}},
		{name: "dependsOn blocks the first apply", events: []string{"flux_apply"}, err: "action flux_apply disabled"},
		{name: "apply waits for the interval", events: []string{"flux_dependenciesReady", "flux_apply", "flux_apply"}, err: "action flux_apply disabled"},
		{name: "interval requeues the apply", events: []string{"flux_dependenciesReady", "flux_apply", "flux_markReady", "flux_tick", "flux_apply"}, want: map[string]any{"flux_now": int64(1), "flux_ready": false}},
		{name: "consumer waits for ready", events: []string{"flux_dependenciesReady", "flux_apply", "consumer_reconcile"}, err: "action consumer_reconcile disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, err := run(t, loaded.Result.Model, tc.events...)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.want {
				if state[k] != v {
					t.Errorf("%s = %v (%T), want %v", k, state[k], state[k], v)
				}
			}
		})
	}
}
