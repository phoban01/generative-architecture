// Package ci checks the repository's GitHub Actions workflows. GitHub runs
// them; these tests only parse them and check the jobs this repository
// relies on, so a broken workflow fails go test locally.
package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const root = "../.."

// Workflow is the part of a GitHub Actions workflow the tests read.
type Workflow struct {
	Name        string            `yaml:"name"`
	On          any               `yaml:"on"`
	Jobs        map[string]Job    `yaml:"jobs"`
	Permissions map[string]string `yaml:"permissions"`
	Concurrency any               `yaml:"concurrency"`
	Env         map[string]string `yaml:"env"`
}

// Job is one workflow job. A reusable-workflow call has Uses and no
// steps.
type Job struct {
	Name     string            `yaml:"name"`
	Needs    any               `yaml:"needs"`
	RunsOn   string            `yaml:"runs-on"`
	Uses     string            `yaml:"uses"`
	With     map[string]any    `yaml:"with"`
	Strategy Strategy          `yaml:"strategy"`
	Steps    []Step            `yaml:"steps"`
	Env      map[string]string `yaml:"env"`
	If       string            `yaml:"if"`
	Perms    map[string]string `yaml:"permissions"`
	Timeout  int               `yaml:"timeout-minutes"`
	Outputs  map[string]string `yaml:"outputs"`
	Secrets  any               `yaml:"secrets"`
	Defaults any               `yaml:"defaults"`
}

// Strategy holds a job's matrix.
type Strategy struct {
	Matrix   map[string]any `yaml:"matrix"`
	FailFast *bool          `yaml:"fail-fast"`
}

// Step is one job step.
type Step struct {
	Name             string            `yaml:"name"`
	Uses             string            `yaml:"uses"`
	Run              string            `yaml:"run"`
	With             map[string]any    `yaml:"with"`
	If               string            `yaml:"if"`
	ID               string            `yaml:"id"`
	Env              map[string]string `yaml:"env"`
	Shell            string            `yaml:"shell"`
	WorkingDirectory string            `yaml:"working-directory"`
}

func readWorkflow(t *testing.T, path string) Workflow {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var w Workflow
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&w); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return w
}

func needs(j Job) []string {
	switch n := j.Needs.(type) {
	case string:
		return []string{n}
	case []any:
		var out []string
		for _, v := range n {
			out = append(out, v.(string))
		}
		return out
	}
	return nil
}

var pinnedAction = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@v[0-9]+(\.[0-9]+)*$`)

// Every workflow parses, has a trigger and jobs, and every job runs
// steps or calls a workflow. Every action is pinned to a version tag,
// every needed job exists and every step either runs a command or uses
// an action.
func TestWorkflowsParse(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	for _, p := range paths {
		w := readWorkflow(t, p)
		if w.Name == "" || w.On == nil || len(w.Jobs) == 0 {
			t.Errorf("%s: needs a name, a trigger and jobs", p)
		}
		for id, j := range w.Jobs {
			if (j.Uses == "") == (len(j.Steps) == 0) {
				t.Errorf("%s: job %s must either run steps or call a workflow", p, id)
			}
			if j.Uses == "" && j.RunsOn == "" {
				t.Errorf("%s: job %s has no runs-on", p, id)
			}
			for _, n := range needs(j) {
				if _, ok := w.Jobs[n]; !ok {
					t.Errorf("%s: job %s needs unknown job %s", p, id, n)
				}
			}
			for i, s := range j.Steps {
				if (s.Uses == "") == (s.Run == "") {
					t.Errorf("%s: job %s step %d must either run a command or use an action", p, id, i)
				}
				if s.Uses != "" && !pinnedAction.MatchString(s.Uses) {
					t.Errorf("%s: job %s uses %q, not pinned to a version tag", p, id, s.Uses)
				}
			}
		}
	}
}

// supportedGo reads the supported Go minor releases from README.md.
func supportedGo(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`currently (1\.[0-9]+) and (1\.[0-9]+)`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("README.md does not list the supported Go releases")
	}
	return []string{m[1] + ".x", m[2] + ".x"}
}

func matrixGo(t *testing.T, j Job) []string {
	t.Helper()
	raw, ok := j.Strategy.Matrix["go"].([]any)
	if !ok {
		t.Fatalf("job %q has no go matrix", j.Name)
	}
	var out []string
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

func runs(j Job) string {
	var b strings.Builder
	for _, s := range j.Steps {
		b.WriteString(s.Run)
		b.WriteString("\n")
	}
	return b.String()
}

// The envtest job runs after the build job on every supported Go release
// and runs make envtest, whose recipe installs the assets, requires them
// and runs go test -p 2.
func TestEnvtestJob(t *testing.T) {
	w := readWorkflow(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	j, ok := w.Jobs["envtest"]
	if !ok {
		t.Fatal("ci.yml has no envtest job")
	}
	if !slices.Contains(needs(j), "go") {
		t.Errorf("envtest needs %v; want the go job", needs(j))
	}
	if got, want := matrixGo(t, j), supportedGo(t); !slices.Equal(got, want) {
		t.Errorf("envtest matrix %v; README.md supports %v", got, want)
	}
	if !strings.Contains(runs(j), "make envtest") {
		t.Errorf("envtest job does not run make envtest:\n%s", runs(j))
	}
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	recipe := string(makefile)[strings.Index(string(makefile), "\nenvtest:"):]
	recipe = recipe[:strings.Index(recipe, "\n\n")]
	for _, want := range []string{"setup-envtest@$(ENVTEST_VERSION)", "ENVTEST_REQUIRED=1", "go test -p 2", "-run Envtest", "--- SKIP"} {
		if !strings.Contains(recipe, want) {
			t.Errorf("make envtest recipe lacks %q", want)
		}
	}
}
