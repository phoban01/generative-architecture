// Package runtimecheck checks a bounded, explicitly configured runtime profile.
package runtimecheck

import (
	"fmt"
	"generative-architecture/examples/machinevendor/architecture"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/sourcegen"
	"golang.org/x/mod/modfile"
	"os"
	"path/filepath"
	"strings"
)

type Profile struct {
	ControllerRuntimeVersion string   `yaml:"controllerRuntimeVersion"`
	ClientGoVersion          string   `yaml:"clientGoVersion"`
	Queue                    string   `yaml:"queue"`
	MaxConcurrentReconciles  int      `yaml:"maxConcurrentReconciles"`
	Key                      string   `yaml:"key"`
	CachedReads              []string `yaml:"cachedReads"`
	DirectReads              []string `yaml:"directReads"`
	RetryDelayTicks          int      `yaml:"retryDelayTicks"`
	RequeueAfterTicks        int      `yaml:"requeueAfterTicks"`
	MaxTicks                 int      `yaml:"maxTicks"`
}

func profilePath(root string) (string, error) {
	var sys architecture.System
	if _, err := krm.Read(filepath.Join(root, "system.yaml"), "System", &sys); err != nil {
		return "", err
	}
	if sys.Runtime == "" {
		return "", fmt.Errorf("System.spec.runtime is required")
	}
	return sourcegen.LocalPath(root, sys.Runtime)
}
func Load(root string) (Profile, error) {
	var p Profile
	path, err := profilePath(root)
	if err != nil {
		return p, err
	}
	_, err = krm.Read(path, "RuntimeProfile", &p)
	if err != nil {
		return p, err
	}
	// Reject options without implemented/tested semantics instead of silently ignoring them.
	if p.ControllerRuntimeVersion != "v0.23.3" || p.ClientGoVersion != "v0.35.0" || p.Queue != "client-go-fifo" || p.MaxConcurrentReconciles != 1 || p.Key != "demo/infra-customer" || strings.Join(p.CachedReads, ",") != "MachineClaim,InfraMachine" || strings.Join(p.DirectReads, ",") != "MachineClaim" || p.RetryDelayTicks != 2 || p.RequeueAfterTicks != 3 || p.MaxTicks != 20 {
		return p, fmt.Errorf("unsupported runtime profile: this adapter implements the checked-in bounded FIFO demo profile only")
	}
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return p, err
	}
	mf, err := modfile.Parse("go.mod", b, nil)
	if err != nil {
		return p, err
	}
	versions := map[string]string{}
	for _, dep := range mf.Require {
		versions[dep.Mod.Path] = dep.Mod.Version
	}
	for mod, v := range map[string]string{"sigs.k8s.io/controller-runtime": p.ControllerRuntimeVersion, "k8s.io/client-go": p.ClientGoVersion} {
		if versions[mod] != v {
			return p, fmt.Errorf("runtime dependency drift for %s", mod)
		}
		for _, replace := range mf.Replace {
			if replace.Old.Path == mod {
				return p, fmt.Errorf("runtime dependency replacement requires review: %s", mod)
			}
		}
	}
	return p, nil
}
func render(p Profile) string {
	return fmt.Sprintf(`// Runtime library model: reviewed semantics, not extracted from controller-runtime.
// One reconcile key, one worker. Logical timers, not wall-clock performance.
// publish is a macro-step calling real MAC.Publish in replay; its internal race
// remains covered separately by the source-extracted Publication model.
module Runtime {
 var s: { ready: bool, processing: bool, dirty: bool, due: int, now: int, retries: int,
          lease: bool, cached: bool, observed: bool, published: bool, accepted: bool, fresh: bool }
 var event: str
 var tick: int
 action init = all {
  s' = { ready: false, processing: false, dirty: false, due: -1, now: 0, retries: 0,
         lease: true, cached: true, observed: false, published: false, accepted: false, fresh: true },
  event' = "init", tick' = 0
 }
 %s
 action change(label, updated) = all {s' = updated, event' = label, tick' = tick + 1}
 action enqueue = change("enqueue", queueEnqueue(s))
 action start = all {s.ready, not(s.processing), change("start",queueStart(s))}
 action finish(outcome) = all {s.processing, change(outcome,queueComplete(s,outcome))}
 action timePasses = all { s.now < %d, change("time",s.with("now",s.now+1)) }
 action fire = all {s.due >= 0, s.now >= s.due,
  change("fire",queueFire(s)) }
 action revoke = change("revoke",s.with("lease",false))
 action syncCache = change("cache-sync",s.with("cached",s.lease))
 action syncInfraCache = change("infra-cache-sync",s.with("fresh",true))
 action observe = all {s.processing,change("observe",s.with("observed",s.cached))}
 action publish = all {s.processing,change("publish",s.with("accepted",s.observed and s.lease and s.fresh).with("published",s.published or (s.observed and s.lease and s.fresh)).with("fresh",s.fresh and not(s.observed and s.lease)))}
 action idle = change("idle",s)
 action step = any {enqueue,start,finish("success"),finish("error"),finish("requeue"),finish("after"),finish("terminal"),finish("error-with-after"),timePasses,fire,revoke,syncCache,syncInfraCache,observe,publish,idle}
 val queueSafety = (not(s.ready and s.processing)) and (not(s.ready) or s.dirty)
 val publicationChecksDirectRead = event != "publish" or not(s.accepted) or s.lease
 val runtimeSafety = queueSafety and publicationChecksDirectRead
 // Intentionally false property: cache delivery can lag API writes.
 val cacheIsAlwaysFresh = s.cached == s.lease
 action publicationStep = if (tick == 0) enqueue else if (tick == 1) start else if (tick == 2) observe else if (tick == 3) publish else if (tick == 4) publish else if (tick == 5) syncInfraCache else if (tick == 6) publish else if (tick == 7) finish("success") else idle
 action staleCacheStep = if (tick == 0) enqueue else if (tick == 1) start else if (tick == 2) revoke else if (tick == 3) observe else if (tick == 4) publish else if (tick == 5) finish("success") else idle
 action dirtyStep = if (tick == 0) enqueue else if (tick == 1) enqueue else if (tick == 2) start else if (tick == 3) enqueue else if (tick == 4) enqueue else if (tick == 5) finish("success") else if (tick == 6) start else if (tick == 7) finish("success") else idle
 action retryStep = if (tick == 0) enqueue else if (tick == 1) start else if (tick == 2) finish("error-with-after") else if (tick == 3) timePasses else if (tick == 4) timePasses else if (tick == 5) fire else if (tick == 6) start else if (tick == 7) finish("after") else if (tick == 8) timePasses else if (tick == 9) timePasses else if (tick == 10) timePasses else if (tick == 11) fire else if (tick == 12) start else if (tick == 13) finish("terminal") else idle
 action timerSurvivesSuccessStep = if (tick == 0) enqueue else if (tick == 1) start else if (tick == 2) finish("after") else if (tick == 3) enqueue else if (tick == 4) start else if (tick == 5) finish("success") else if (tick == 6) timePasses else if (tick == 7) timePasses else if (tick == 8) timePasses else if (tick == 9) fire else if (tick == 10) start else if (tick == 11) finish("success") else idle
}
`, queueHelpers(p), p.MaxTicks)
}
func Generate(root string) (string, error) {
	p, err := Load(root)
	if err != nil {
		return "", err
	}
	q := render(p)

	dir := filepath.Join(root, "generated/runtime")
	if err = os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(root, "generated/runtime-overview.c4"), []byte(runtimeDiagram), 0644); err != nil {
		return "", err
	}
	return q, os.WriteFile(filepath.Join(dir, "Runtime.qnt"), []byte(q), 0644)
}
