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
	"generative-architecture/pkg/report"
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
	options := quint.Options{Samples: 40, Steps: 60, Seed: 20261013}
	samples, err := quint.Sample(ctx, m, options)
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
	v := report.Verification{
		Name:        "multiple-controllers",
		Mode:        report.ModeStrict,
		Scope:       "bounded runtime simulation; real FIFO workers; scripted reconcile outcomes",
		Sampling:    &report.Sampling{Samples: options.Samples, Steps: options.Steps, Seed: options.Seed},
		Traces:      len(samples.Traces),
		Transitions: steps,
		Coverage:    coverage,
	}
	v.Conclude()
	if blocking := v.Blocking(); len(blocking) > 0 {
		return fmt.Errorf("%s finding: %s", blocking[0].Category, blocking[0].Message)
	}
	if err = v.Write(filepath.Join(root, "report.yaml")); err != nil {
		return err
	}
	fmt.Printf("PASS reusable runtime: %d traces, %d transitions\n", len(samples.Traces), steps)
	return nil
}
