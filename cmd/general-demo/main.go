// general-demo checks the reusable runtime model against real controller-runtime
// queues. Business reconcilers are tested separately; these outcomes are scripted.
package main

import (
	"context"
	"fmt"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/quint"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	root := "examples/runtimegroup"
	var profile cr.GroupProfile
	if _, err := krm.Read(filepath.Join(root, "profile.yaml"), "RuntimeGroup", &profile); err != nil {
		return err
	}
	generated, err := cr.BuildGroupModel(profile)
	if err != nil {
		return err
	}
	m, err := model.Compile(generated.Spec)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "generated")
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "Model.qnt"), []byte(m.Quint()), 0644); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	samples, err := quint.Sample(ctx, m, quint.Options{Samples: 40, Steps: 60, Seed: 20261013})
	if err != nil {
		return err
	}
	steps := 0
	coverage := map[string]int{}
	for i, trace := range samples.Traces {
		report, err := cr.ReplayGroup(ctx, generated, trace)
		if err != nil {
			return fmt.Errorf("trace %d: %w", i, err)
		}
		steps += report.Steps
		for name, n := range report.Coverage {
			coverage[name] += n
		}
	}
	// Native targeted tests guarantee overlapping invocations and dirty keys;
	// sampled schedules broaden coverage without claiming exhaustiveness.
	report := map[string]any{"result": "passed", "traces": len(samples.Traces), "transitions": steps, "coverage": coverage, "scope": "bounded runtime simulation; real FIFO workers; scripted reconcile outcomes"}
	if err = krm.WriteResult(filepath.Join(root, "report.yaml"), "VerificationReport", "multiple-controllers", report); err != nil {
		return err
	}
	fmt.Printf("PASS reusable runtime: %d traces, %d transitions\n", len(samples.Traces), steps)
	return nil
}
