package behaviorgen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"generative-architecture/examples/machinevendor/architecture"
	"generative-architecture/pkg/sourcegen"
)

func loadConfig(root string) (Config, error) { return loadConfigAt(root, "behavior/publication.yaml") }
func loadConfigAt(root, analysisPath string) (Config, error) {
	var cfg Config
	path, err := sourcegen.LocalPath(root, analysisPath)
	if err != nil {
		return cfg, err
	}
	if err := architecture.ReadResource(path, "BehaviorAnalysis", &cfg); err != nil {
		return cfg, err
	}
	project, err := architecture.Load(root)
	if err != nil {
		return cfg, err
	}
	if _, err := project.Compile(); err != nil {
		return cfg, err
	}
	if project.Sources == nil {
		return cfg, fmt.Errorf("behavior extraction requires source descriptors")
	}
	for _, controller := range project.Sources.Controllers {
		if controller.Component != cfg.Component {
			continue
		}
		for _, action := range controller.Actions {
			if action.Name == cfg.Action {
				cfg.Source = action.Source.File
				cfg.Method = action.Method
				parts := strings.Split(controller.GoType, ".")
				cfg.Receiver = parts[len(parts)-1]
				return cfg, nil
			}
		}
	}
	return cfg, fmt.Errorf("no source method for %s.%s", cfg.Component, cfg.Action)
}

func Generate(root string) (*Program, *Program, error) {
	_ = os.Remove(filepath.Join(root, "reports/behavior/summary.yaml"))
	if err := architecture.ExtractSources(root); err != nil {
		return nil, nil, err
	}
	cfg, err := loadConfig(root)
	if err != nil {
		return nil, nil, err
	}
	path, err := sourcegen.LocalPath(root, cfg.Source)
	if err != nil {
		return nil, nil, err
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	original, err := Extract(source, cfg)
	if err != nil {
		return nil, nil, err
	}
	mutantSource, err := RemoveStaleCheck(source, cfg)
	if err != nil {
		return nil, nil, err
	}
	mutant, err := Extract(mutantSource, cfg)
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(root, "generated/behavior")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, nil, err
	}
	for name, p := range map[string]*Program{"Publication": original, "PublicationMutant": mutant} {
		q, err := p.Quint()
		if err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, name+".qnt"), []byte(q), 0644); err != nil {
			return nil, nil, err
		}
		if err := architecture.WriteResult(filepath.Join(dir, name+".yaml"), "BehaviorProgram", strings.ToLower(name), p); err != nil {
			return nil, nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "mac_mutant.go.txt"), mutantSource, 0644); err != nil {
		return nil, nil, err
	}
	return original, mutant, nil
}
func run(root, tool, log string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if e := os.WriteFile(filepath.Join(root, "reports/behavior", log+".txt"), output, 0644); e != nil {
		return output, e
	}
	return output, err
}
func traceSummary(path string) ([]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	decoded, err := architecture.DecodeITF(raw)
	if err != nil {
		return nil, err
	}
	m := decoded.(map[string]any)
	states, ok := m["states"].([]any)
	if !ok || len(states) == 0 {
		return nil, fmt.Errorf("empty counterexample")
	}
	return states, nil
}

// Check demonstrates three distinct results: source-derived snapshot checking,
// a real source mutation, and the unmutated method's separate read/write race.
func Check(root, tool string) (map[string]any, error) {
	reports := filepath.Join(root, "reports/behavior")
	if err := os.MkdirAll(reports, 0755); err != nil {
		return nil, err
	}
	summaryPath := filepath.Join(reports, "summary.yaml")
	_ = os.Remove(summaryPath)
	original, mutant, err := Generate(root)
	if err != nil {
		return nil, err
	}
	if tool == "" {
		tool = os.Getenv("QUINT_BIN")
	}
	if tool == "" {
		tool = "quint"
	}
	version, err := run(root, tool, "version", "--version")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(version)) != "0.33.0" {
		return nil, fmt.Errorf("expected Quint 0.33.0")
	}
	for _, name := range []string{"Publication", "PublicationMutant"} {
		output, err := run(root, tool, name+"-typecheck", "typecheck", "generated/behavior/"+name+".qnt")
		if err != nil {
			return nil, fmt.Errorf("typecheck: %s: %w", output, err)
		}
	}
	tests := []struct {
		name, model, step, property string
		samples                     int
		violation                   bool
	}{
		{"snapshot-check", "Publication", "step", "publicationUsesCheckedLease", 500, false},
		{"intact-stale-lease", "Publication", "staleStep", "publicationUsesCheckedLease", 1, false},
		{"intact-stale-epoch", "Publication", "staleEpochStep", "publicationUsesCheckedLease", 1, false},
		{"removed-check-stale-lease", "PublicationMutant", "staleStep", "publicationUsesCheckedLease", 1, true},
		{"removed-check-stale-epoch", "PublicationMutant", "staleEpochStep", "publicationUsesCheckedLease", 1, true},
		{"intact-read-write-race", "Publication", "revocationAfterReadStep", "publicationUsesLiveLease", 1, true},
	}
	results := map[string]any{}
	for _, test := range tests {
		traceName := "reports/behavior/" + test.name + ".itf.json"
		_ = os.Remove(filepath.Join(root, traceName))
		args := []string{"run", "generated/behavior/" + test.model + ".qnt", "--backend", "typescript", "--step", test.step, "--max-steps", "30", "--max-samples", fmt.Sprint(test.samples), "--seed", "42", "--invariant", test.property, "--verbosity", "1"}
		if test.samples == 1 {
			args = append(args, "--init", "scenarioInit")
		}
		if test.violation {
			args = append(args, "--out-itf", traceName)
		}
		output, err := run(root, tool, test.name, args...)
		result := map[string]any{"property": test.property, "model": test.model, "expectedViolation": test.violation, "samples": test.samples}
		if test.violation {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				return nil, fmt.Errorf("expected counterexample for %s: %s: %v", test.name, output, err)
			}
			var metadata struct {
				Meta struct {
					Status string `json:"status"`
				} `json:"#meta"`
			}
			b, readErr := os.ReadFile(filepath.Join(root, traceName))
			if readErr != nil {
				return nil, readErr
			}
			if err := json.Unmarshal(b, &metadata); err != nil {
				return nil, err
			}
			if metadata.Meta.Status != "violation" {
				return nil, fmt.Errorf("expected invariant violation, not tool error")
			}
			states, e := traceSummary(filepath.Join(root, traceName))
			if e != nil {
				return nil, e
			}
			last := states[len(states)-1].(map[string]any)
			if last["event"] != "commit" {
				return nil, fmt.Errorf("counterexample must occur at publication commit")
			}
			result["trace"] = traceName
			result["steps"] = len(states) - 1
			result["finalState"] = last
			if err := architecture.WriteResult(filepath.Join(reports, test.name+".yaml"), "Counterexample", test.name, states); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, fmt.Errorf("unexpected failure %s: %s: %w", test.name, output, err)
		}
		results[test.name] = result
		fmt.Printf("PASS %s (expected violation: %v)\n", test.name, test.violation)
	}
	configBytes, err := os.ReadFile(filepath.Join(root, "behavior/publication.yaml"))
	if err != nil {
		return nil, err
	}
	originalModel, _ := original.Quint()
	mutantModel, _ := mutant.Quint()
	summary := map[string]any{"configurationSha256": hash(configBytes), "originalModelSha256": hash([]byte(originalModel)), "mutantModelSha256": hash([]byte(mutantModel)), "implementation": "Go AST -> control-flow transitions -> Quint", "source": original.cfg.Source, "method": original.cfg.Receiver + "." + original.cfg.Method, "sourceSha256": original.SourceSHA256, "originalMethodSha256": original.MethodSHA256, "mutantMethodSha256": mutant.MethodSHA256, "results": results, "scope": "single subreconciler-like process; finite environment summaries, sampled simulation, not exhaustive verification", "atomicity": "API read and write are separate transitions; observed snapshot retained between them"}
	if err := architecture.WriteResult(summaryPath, "VerificationReport", "behavior", summary); err != nil {
		return nil, err
	}
	return summary, nil
}

// LoadProgram extracts the selected method without rewriting reports or artifacts.
// Source descriptors must already match the checked-in source inventory.
func LoadProgram(root, analysisPath string) (*Program, error) {
	cfg, err := loadConfigAt(root, analysisPath)
	if err != nil {
		return nil, err
	}
	path, err := sourcegen.LocalPath(root, cfg.Source)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Extract(b, cfg)
}
