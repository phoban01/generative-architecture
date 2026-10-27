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

func TestFluxSourceAndHelmBoundaries(t *testing.T) {
	for _, tc := range []struct {
		path, upstream, version string
		assumptions             map[string]ir.Evidence
	}{
		{path: "flux/gitrepository.yaml", upstream: "github.com/fluxcd/source-controller", version: "v1.4.1", assumptions: map[string]ir.Evidence{"interval": ir.Declared, "artifactRevision": ir.Declared, "readyCondition": ir.Declared, "monotoneRevision": ir.Assumed}},
		{path: "flux/helmrelease.yaml", upstream: "github.com/fluxcd/helm-controller", version: "v1.1.0", assumptions: map[string]ir.Evidence{"interval": ir.Declared, "dependsOn": ir.Declared, "installOrUpgrade": ir.Declared, "releaseStorage": ir.Declared, "noRemediation": ir.Assumed}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			b := loadLibrary(t, tc.path)
			if b.Spec.Upstream != tc.upstream || b.Spec.Version != tc.version {
				t.Fatalf("pin %s@%s", b.Spec.Upstream, b.Spec.Version)
			}
			got := map[string]ir.Evidence{}
			for _, a := range b.Spec.Assumptions {
				got[a.ID] = a.Evidence
			}
			if len(got) != len(tc.assumptions) {
				t.Fatalf("assumptions %v, want %v", got, tc.assumptions)
			}
			for id, e := range tc.assumptions {
				if got[id] != e {
					t.Errorf("assumption %s evidence %q, want %q", id, got[id], e)
				}
			}
		})
	}
}

// fluxConsumer is an owned controller that acts once a source revision, the
// applied manifests and a Helm release all exist.
func fluxConsumer() (model.Spec, map[string]composition.Port) {
	spec := model.Spec{Name: "Consumer", State: map[string]model.Variable{
		"revision":  {Type: "int", Initial: "0"},
		"manifests": {Type: "bool", Initial: "false"},
		"release":   {Type: "int", Initial: "0"},
		"done":      {Type: "bool", Initial: "false"},
	}, Actions: []model.Action{{Name: "reconcile", Guard: "revision > 0 && manifests && release > 0 && !done", Updates: map[string]expr.Expression{"done": "true"}}}}
	ports := map[string]composition.Port{
		"source":    {State: "revision", Contract: composition.ResourceContract(ir.GVK{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "GitRepository"}), Version: composition.ResourceContractVersion, Direction: "in"},
		"manifests": {State: "manifests", Contract: "kustomize.toolkit.fluxcd.io/applied", Version: "v1", Direction: "in"},
		"release":   {State: "release", Contract: "helm.toolkit.fluxcd.io/released", Version: "v1", Direction: "in"},
	}
	return spec, ports
}

func TestFluxSystemComposes(t *testing.T) {
	consumer, ports := fluxConsumer()
	loaded, err := system{boundaries: map[string]string{"git": "flux/gitrepository.yaml", "helm": "flux/helmrelease.yaml", "kustomize": "flux/kustomization.yaml"}, consumer: consumer, ports: ports}.compose(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range loaded.Components[:3] {
		if c.Evidence != ir.Assumed {
			t.Errorf("%s evidence %q", c.Name, c.Evidence)
		}
	}
	setup := func(more ...string) []string {
		return append([]string{"git_fetch", "kustomize_dependenciesReady", "kustomize_apply", "helm_dependenciesReady", "helm_install"}, more...)
	}
	for _, tc := range []struct {
		name   string
		events []string
		err    string
		want   map[string]any
	}{
		{name: "consumer acts after source, apply and install", events: setup("consumer_reconcile"), want: map[string]any{"consumer_done": true, "git_revision": int64(1), "helm_releaseVersion": int64(1)}},
		{name: "consumer waits for the source", events: []string{"kustomize_dependenciesReady", "kustomize_apply", "helm_dependenciesReady", "helm_install", "consumer_reconcile"}, err: "action consumer_reconcile disabled"},
		{name: "new commit advances the revision on the next interval", events: setup("git_commit", "git_tick", "git_fetch"), want: map[string]any{"git_revision": int64(2)}},
		{name: "fetch waits for the interval", events: []string{"git_fetch", "git_commit", "git_fetch"}, err: "action git_fetch disabled"},
		{name: "chart change upgrades the release", events: setup("helm_changeChart", "helm_tick", "helm_upgrade"), want: map[string]any{"helm_releaseVersion": int64(2), "helm_appliedChart": int64(2)}},
		{name: "unchanged chart does not upgrade", events: setup("helm_tick", "helm_upgrade"), err: "action helm_upgrade disabled"},
		{name: "install waits for dependsOn", events: []string{"helm_install"}, err: "action helm_install disabled"},
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
					t.Errorf("%s = %v, want %v", k, state[k], v)
				}
			}
		})
	}
}
