package controllerdemo

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"generative-architecture/examples/machinevendor/architecture"
	"generative-architecture/generated"
)

// Check first verifies the existing evidence through the native pipeline, then
// replays the exact same traces against the independently authored MAC adapter.
func Check(root string) (map[string]any, error) {
	reportPath := filepath.Join(root, "reports/controller-demo.yaml")
	if err := os.Remove(reportPath); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	p, err := architecture.Load(root)
	if err != nil {
		return nil, err
	}
	m, err := p.Compile()
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(m.Quint())))
	if hash != generated.ModelSHA256 {
		return nil, fmt.Errorf("compiled adapter is stale; run arch generate and rebuild controller-demo")
	}
	if _, err = m.Check(architecture.CheckOptions{Replay: true}); err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(root, "traces/*.itf.json"))
	if err != nil {
		return nil, err
	}
	coverage := map[string]int{}
	steps := 0
	replay := func(path string, mutation bool) (architecture.ReplayResult, error) {
		trace, err := architecture.ReadTrace(path)
		if err != nil {
			return architecture.ReplayResult{}, err
		}
		adapter, err := New(p.System, mutation)
		if err != nil {
			return architecture.ReplayResult{}, err
		}
		return m.ReplayAdapter(trace, adapter)
	}
	for _, path := range paths {
		result, err := replay(path, false)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		if !result.OK {
			return nil, fmt.Errorf("%s: controller mismatch at step %d (%s): expected=%v actual=%v", filepath.Base(path), result.Steps, result.Action, result.Expected, result.Actual)
		}
		steps += result.Steps
		for action, count := range result.Coverage {
			coverage[action] += count
		}
	}
	mutations := map[string]any{}
	for _, name := range []string{"staleLease", "staleEpoch"} {
		result, err := replay(filepath.Join(root, "traces", name+".itf.json"), true)
		if err != nil {
			return nil, err
		}
		if result.OK || result.Action != "mac.publish" || result.ExpectedAccepted || !result.ActualAccepted {
			return nil, fmt.Errorf("controller mutation %s not detected as expected", name)
		}
		mutations[name] = map[string]any{"detected": true, "step": result.Steps, "action": result.Action}
	}
	race, err := replay(filepath.Join(root, "reports/revocation-race.itf.json"), false)
	if err != nil {
		return nil, err
	}
	if !race.OK {
		return nil, fmt.Errorf("counterexample did not replay against controller adapter")
	}
	summary := map[string]any{
		"modelSha256": hash, "traces": len(paths), "transitions": steps, "coverage": coverage, "mutations": mutations,
		"implementation":           "handwritten demo MAC reconcile phases using controller-runtime v0.23.3 fake client",
		"execution":                "phase-scheduled; no manager, watch queue, real API server or envtest",
		"environment":              "handwritten claim/lease, InfraMachine submission and machine service fakes",
		"counterexampleReproduced": true, "productionCAPIConformance": false,
	}
	if err := architecture.WriteResult(reportPath, "VerificationReport", "controller-demo", summary); err != nil {
		return nil, err
	}
	fmt.Printf("PASS controller demo: %d traces, %d transitions; stale-lease and stale-epoch mutations detected; expected safety counterexample reproduced\n", len(paths), steps)
	return summary, nil
}
