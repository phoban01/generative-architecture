package ci

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"generative-architecture/pkg/report"
)

var (
	archWorkflow = filepath.Join(root, ".github", "workflows", "arch.yml")
	ciWorkflow   = filepath.Join(root, ".github", "workflows", "ci.yml")
)

// Input is one input of a reusable workflow.
type Input struct {
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required"`
	Default     any    `yaml:"default"`
}

func callInputs(t *testing.T, path string) map[string]Input {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		On struct {
			WorkflowCall struct {
				Inputs map[string]Input `yaml:"inputs"`
			} `yaml:"workflow_call"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	if len(w.On.WorkflowCall.Inputs) == 0 {
		t.Fatalf("%s is not a reusable workflow with inputs", path)
	}
	return w.On.WorkflowCall.Inputs
}

var (
	expression = regexp.MustCompile(`\$\{\{\s*(.*?)\s*\}\}`)
	envRef     = regexp.MustCompile(`\$\{?(ARCH_[A-Z0-9_]+)`)
	envExpr    = regexp.MustCompile(`^inputs\.([a-z0-9-]+)( \|\| github\.base_ref)?$`)
)

// The reusable workflow keeps every input out of its scripts: the job
// environment carries them, scripts read only variables it defines, and
// the job environment reads only declared inputs.
func TestArchWorkflowShape(t *testing.T) {
	inputs := callInputs(t, archWorkflow)
	w := readWorkflow(t, archWorkflow)
	if len(w.Jobs) != 1 {
		t.Fatalf("arch.yml has %d jobs; want one", len(w.Jobs))
	}
	var job Job
	for _, j := range w.Jobs {
		job = j
	}
	for name, value := range job.Env {
		m := expression.FindStringSubmatch(value)
		if m == nil || m[0] != value {
			t.Errorf("env %s = %q; want one expression", name, value)
			continue
		}
		in := envExpr.FindStringSubmatch(m[1])
		if in == nil {
			t.Errorf("env %s reads %q; only inputs and github.base_ref are allowed", name, m[1])
			continue
		}
		if _, ok := inputs[in[1]]; !ok {
			t.Errorf("env %s reads undeclared input %s", name, in[1])
		}
	}
	var names []string
	for _, s := range job.Steps {
		names = append(names, s.Name)
		if strings.Contains(s.Run, "${{") {
			t.Errorf("step %q expands an expression inside its script", s.Name)
		}
		for _, m := range envRef.FindAllStringSubmatch(s.Run, -1) {
			if _, ok := job.Env[m[1]]; !ok {
				t.Errorf("step %q reads %s, which the job does not set", s.Name, m[1])
			}
		}
	}
	order := []string{"Install arch", "Install Quint and LikeC4", "Extract", "Generate", "Check", "Replay", "Render diagrams", "Diff IR against the base branch", "Upload architecture artifacts"}
	var got []string
	for _, n := range names {
		if n != "" {
			got = append(got, n)
		}
	}
	if !slices.Equal(got, order) {
		t.Errorf("steps %v; want %v", got, order)
	}
	upload := job.Steps[len(job.Steps)-1]
	if !strings.HasPrefix(upload.Uses, "actions/upload-artifact@") || upload.If != "${{ !cancelled() }}" || upload.With["path"] != "${{ inputs.out }}" {
		t.Errorf("upload step %+v must upload inputs.out even after a failure", upload)
	}
	// The default tool versions are the pins in hack/tools/package.json.
	b, err := os.ReadFile(filepath.Join(root, "hack", "tools", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatal(err)
	}
	for input, dep := range map[string]string{"quint-version": "@informalsystems/quint", "likec4-version": "likec4"} {
		if got := fmt.Sprint(inputs[input].Default); got != pkg.Dependencies[dep] {
			t.Errorf("input %s defaults to %s; hack/tools pins %s", input, got, pkg.Dependencies[dep])
		}
	}
}

// The repository's own pipeline job calls arch.yml on ConfigMirror after
// the build job, with tools from hack/tools and only declared inputs.
func TestArchitectureJob(t *testing.T) {
	inputs := callInputs(t, archWorkflow)
	w := readWorkflow(t, ciWorkflow)
	j, ok := w.Jobs["architecture"]
	if !ok {
		t.Fatal("ci.yml has no architecture job")
	}
	if j.Uses != "./.github/workflows/arch.yml" || !slices.Contains(needs(j), "go") {
		t.Fatalf("architecture job uses %q and needs %v", j.Uses, needs(j))
	}
	for name := range j.With {
		if _, ok := inputs[name]; !ok {
			t.Errorf("architecture job passes undeclared input %s", name)
		}
	}
	for name, in := range inputs {
		if _, ok := j.With[name]; in.Required && !ok {
			t.Errorf("architecture job lacks required input %s", name)
		}
	}
	if j.With["tools-dir"] != "hack/tools" || j.With["arch-package"] != "./cmd/arch" {
		t.Errorf("architecture job inputs %v", j.With)
	}
	if _, err := os.Stat(filepath.Join(root, fmt.Sprint(j.With["project"]))); err != nil {
		t.Errorf("project: %v", err)
	}
}

// TestArchPipeline runs the script steps of arch.yml, as the
// architecture job in ci.yml calls it, against ConfigMirror into a
// temporary directory. It stands in for the GitHub runner: steps that use
// an action are skipped, and the Quint and LikeC4 CLIs come from QUINT_BIN
// and LIKEC4_BIN unless ARCH_PIPELINE_NPM is set. It runs only with
// ARCH_PIPELINE set, as make arch-pipeline does. ARCH_PIPELINE_BASE names
// a branch to diff the IR against.
func TestArchPipeline(t *testing.T) {
	if os.Getenv("ARCH_PIPELINE") == "" {
		t.Skip("ARCH_PIPELINE not set; run make arch-pipeline")
	}
	npm := os.Getenv("ARCH_PIPELINE_NPM") != ""
	if !npm && (os.Getenv("QUINT_BIN") == "" || os.Getenv("LIKEC4_BIN") == "") {
		t.Fatal("QUINT_BIN and LIKEC4_BIN are required without ARCH_PIPELINE_NPM")
	}
	inputs := callInputs(t, archWorkflow)
	caller := readWorkflow(t, ciWorkflow).Jobs["architecture"]
	value := func(name string) string {
		if v, ok := caller.With[name]; ok {
			return fmt.Sprint(v)
		}
		if d := inputs[name].Default; d != nil {
			return fmt.Sprint(d)
		}
		return ""
	}
	temp := t.TempDir()
	runner := filepath.Join(temp, "runner")
	out := filepath.Join(temp, "arch-out")
	if err := os.MkdirAll(runner, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range []string{"GITHUB_ENV", "GITHUB_PATH", "GITHUB_STEP_SUMMARY"} {
		files[f] = filepath.Join(temp, f)
		if err := os.WriteFile(files[f], nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"RUNNER_TEMP": runner}
	for k, v := range files {
		env[k] = v
	}
	var job Job
	for _, j := range readWorkflow(t, archWorkflow).Jobs {
		job = j
	}
	for name, expr := range job.Env {
		in := envExpr.FindStringSubmatch(expression.FindStringSubmatch(expr)[1])
		env[name] = value(in[1])
		if in[2] != "" && env[name] == "" {
			env[name] = os.Getenv("ARCH_PIPELINE_BASE")
		}
	}
	env["ARCH_OUT"] = out
	path := os.Getenv("PATH")
	for _, s := range job.Steps {
		if s.Uses != "" {
			continue
		}
		script := s.Run
		if s.Name == "Install Quint and LikeC4" && !npm {
			script = fmt.Sprintf("echo QUINT_BIN=%s >> \"$GITHUB_ENV\"\necho LIKEC4_BIN=%s >> \"$GITHUB_ENV\"\n", os.Getenv("QUINT_BIN"), os.Getenv("LIKEC4_BIN"))
		}
		cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", script)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "PATH="+path)
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		b, err := cmd.CombinedOutput()
		t.Logf("step %s:\n%s", s.Name, tail(string(b), 8))
		if err != nil {
			t.Fatalf("step %s: %v", s.Name, err)
		}
		// Apply what the step appended to GITHUB_ENV and GITHUB_PATH, as
		// the runner does before the next step.
		for k, v := range readLines(t, files["GITHUB_ENV"]) {
			env[k] = v
		}
		for _, dir := range readPath(t, files["GITHUB_PATH"]) {
			if !strings.Contains(path, dir) {
				path = dir + string(os.PathListSeparator) + path
			}
		}
	}
	v, err := report.ReadVerification(filepath.Join(out, "report.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Result != report.Passed || v.Traces != 40 || v.Receipt.Sampling == nil || v.Receipt.Sampling.Strategy != "weighted" || v.Receipt.Sampling.Seed != 20261013 {
		t.Fatalf("check report %s, %d traces, sampling %+v", v.Result, v.Traces, v.Receipt.Sampling)
	}
	if v.Coverage["writeTarget_ok"] == 0 {
		t.Errorf("steered check never takes writeTarget_ok: %v", v.Coverage)
	}
	for _, f := range []string{"ir.yaml", "receipts.yaml", "replay.yaml", "likec4/model.c4", "site/index.html", "ir-diff.txt"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("artifact %s: %v", f, err)
		}
	}
}

func readLines(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		if k, v, ok := strings.Cut(s.Text(), "="); ok {
			out[k] = v
		}
	}
	return out
}

func readPath(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
