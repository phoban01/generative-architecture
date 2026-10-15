// library-demo produces ConfigMirror's evidence from its generated model:
// it generates the model from the controller source, the abstraction and
// the built-in summaries, samples traces of it with Quint, replays them
// through the generated replay adapter, runs the mutation suite and writes
// the report and the input-closure receipt.
package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"generative-architecture/examples/configmirror"
	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/quint"
	"generative-architecture/pkg/receipt"
)

func main() {
	root := flag.String("root", "examples/configmirror", "example directory, relative to the module root (the working directory)")
	refresh := flag.Bool("refresh", false, "sample fresh traces using Quint")
	generateOnly := flag.Bool("generate-only", false, "generate model and diagram without replay")
	flag.Parse()
	if flag.NArg() != 0 || (*refresh && *generateOnly) {
		fmt.Fprintln(os.Stderr, "unexpected arguments or incompatible flags")
		os.Exit(2)
	}
	if err := run(*root, *refresh, *generateOnly); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Sampling bounds: how many Quint traces, of how many steps, from which seed.
const (
	samples = 40
	steps   = 60
	seed    = 20261013
)

func run(root string, refresh, generateOnly bool) error {
	reportPath := filepath.Join(root, configmirror.ReportFile)
	if err := os.Remove(reportPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	spec, err := configmirror.Generate(root)
	if err != nil {
		return err
	}
	m, err := model.Compile(spec)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(root, "generated"), 0755); err != nil {
		return err
	}
	if err = krm.Write(filepath.Join(root, configmirror.SpecFile), "TransitionModel", "config-mirror", spec); err != nil {
		return err
	}
	for file, contents := range map[string]string{configmirror.ModelFile: m.Quint(), configmirror.DiagramFile: m.LikeC4()} {
		if err = os.WriteFile(filepath.Join(root, file), []byte(contents), 0644); err != nil {
			return err
		}
	}
	if generateOnly {
		fmt.Println("Generated ConfigMirror model and diagram")
		return nil
	}
	quintVersion, err := traces(root, m, refresh)
	if err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(root, configmirror.TraceGlob))
	if err != nil {
		return err
	}
	if len(paths) != samples {
		return fmt.Errorf("expected %d traces; use --refresh", samples)
	}
	total := 0
	coverage := map[string]int{}
	hashes := map[string]string{}
	for _, p := range paths {
		trace, err := conformance.ReadITF(p)
		if err != nil {
			return err
		}
		report, err := configmirror.Replay(context.Background(), m, trace)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		total += report.Steps
		for event, n := range report.Coverage {
			coverage[event] += n
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		hashes[filepath.Base(p)] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	observed, err := observedVariables(m)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	suite, matrix, err := configmirror.RunMutations(ctx, ".", root)
	if err != nil {
		return err
	}
	if escaped := suite.Escaped(matrix); len(escaped) > 0 {
		return fmt.Errorf("mutations escaped: %v\n%s", escaped, matrix)
	}

	abs, err := abstraction.Load(filepath.Join(root, "abstraction.yaml"))
	if err != nil {
		return err
	}
	tools := map[string]string{"quint": quintVersion, "go": runtime.Version()}
	var extra []string
	for _, f := range []string{"project.yaml", "abstraction.yaml", "constraints.yaml", "discovery.json", configmirror.MutationsFile} {
		extra = append(extra, filepath.ToSlash(filepath.Join(root, f)))
	}
	r, err := receipt.Collect(".", []string{"./" + filepath.ToSlash(root) + "/...", "./cmd/library-demo"}, extra, tools)
	if err != nil {
		return err
	}
	r.Bounds = bounds(abs.Spec.Bounds)
	r.Excludes = abs.Spec.Excludes
	if err = receipt.Write(filepath.Join(root, configmirror.ReceiptFile), "config-mirror", r); err != nil {
		return err
	}

	report := map[string]any{
		"result":      "passed",
		"model":       configmirror.ModelFile,
		"modelSha256": fmt.Sprintf("%x", sha256.Sum256([]byte(m.Quint()))),
		"sampling":    map[string]any{"samples": samples, "steps": steps, "seed": seed},
		"traces":      len(paths),
		"transitions": total,
		"coverage":    coverage,
		"traceHashes": hashes,
		"observed":    observed,
		"bounds":      abs.Spec.Bounds,
		"excludes":    abs.Spec.Excludes,
		"mutations":   matrix,
		"tools":       tools,
		"receipt":     configmirror.ReceiptFile,
		"scope":       "bounded simulation of the model generated from Reconcile, abstraction.yaml and the built-in client summaries; real worker/FIFO; fake API; controlled cache; observed object, cache and queue state",
	}
	if err = krm.WriteResult(reportPath, "VerificationReport", "config-mirror", report); err != nil {
		return err
	}
	fmt.Printf("PASS config mirror: %d traces, %d transitions, Quint %s\n%s", len(paths), total, quintVersion, matrix)
	return nil
}

// traces samples fresh traces with Quint when refresh is set and returns
// the Quint version that produced the traces in use: the running one, or
// the one the previous receipt recorded.
func traces(root string, m *model.Compiled, refresh bool) (string, error) {
	if !refresh {
		previous, err := receipt.Read(filepath.Join(root, configmirror.ReceiptFile), ".")
		if err != nil {
			return "", fmt.Errorf("%w; use --refresh", err)
		}
		if previous.Tools["quint"] == "" {
			return "", fmt.Errorf("%s records no Quint version; use --refresh", configmirror.ReceiptFile)
		}
		return previous.Tools["quint"], nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := quint.Sample(ctx, m, quint.Options{Samples: samples, Steps: steps, Seed: seed})
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "traces")
	if err = os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	old, err := filepath.Glob(filepath.Join(dir, "*.itf.json"))
	if err != nil {
		return "", err
	}
	for _, p := range old {
		if err = os.Remove(p); err != nil {
			return "", err
		}
	}
	for i, b := range result.ITF {
		if err = os.WriteFile(filepath.Join(dir, fmt.Sprintf("trace-%03d.itf.json", i)), b, 0644); err != nil {
			return "", err
		}
	}
	return result.Version, nil
}

// observedVariables lists the model variables replay compares.
func observedVariables(m *model.Compiled) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, err := configmirror.New(ctx, m)
	if err != nil {
		return nil, err
	}
	observed := a.Observed()
	return observed, a.Close()
}

// bounds repeats the abstraction's bounds under their document names.
func bounds(b abstraction.Bounds) map[string]int {
	out := map[string]int{}
	if b.MaxObjectsPerResource > 0 {
		out["maxObjectsPerResource"] = b.MaxObjectsPerResource
	}
	if b.MaxInvocations > 0 {
		out["maxInvocations"] = b.MaxInvocations
	}
	return out
}
