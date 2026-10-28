package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/project"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// cronJobDir holds the pinned kubebuilder CronJob tutorial controller
// (Checkpoint B). PIN.md records its source and pin.
const cronJobDir = "../../testdata/external/cronjob"

// updateExternal rewrites the committed pipeline report of the pinned
// controller instead of comparing it.
var updateExternal = flag.Bool("update-external", false, "rewrite testdata/external/cronjob/report.yaml")

// TestExternalCronJobPinned checks that every copied upstream file still
// has the hash recorded at the pin, so nobody edits the upstream source.
func TestExternalCronJobPinned(t *testing.T) {
	f, err := os.Open(filepath.Join(cronJobDir, "upstream.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	s := bufio.NewScanner(f)
	for s.Scan() {
		want, name, ok := strings.Cut(s.Text(), "  ")
		if !ok {
			t.Fatalf("malformed checksum line %q", s.Text())
		}
		b, err := os.ReadFile(filepath.Join(cronJobDir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != want {
			t.Errorf("%s differs from the pinned upstream file: sha256 %s, want %s", name, got, want)
		}
		n++
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("upstream.sha256 lists no files")
	}
}

// TestExternalCronJobBuilds builds the pinned controller as its own module
// from the local module cache only.
func TestExternalCronJobBuilds(t *testing.T) {
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = cronJobDir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOFLAGS=-mod=readonly")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build in %s: %v\n%s", cronJobDir, err, out)
	}
}

// TestExternalCronJobProject loads the project document of the pinned
// controller and checks that it resolves the separate module.
func TestExternalCronJobProject(t *testing.T) {
	p, err := project.Load(filepath.Join(cronJobDir, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(cronJobDir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != want || p.Spec.Module != "tutorial.kubebuilder.io/project" {
		t.Fatalf("project resolved module %s at %s, want %s", p.Spec.Module, p.Root, want)
	}
	if _, err := p.LoadCatalog(); err != nil {
		t.Fatal(err)
	}
}

// TestExternalCronJobSupportReportIsCurrent runs arch extract on the pinned
// controller and checks that the committed support report matches it byte
// for byte. Extraction fails closed, so no IR may exist next to it, and
// every unsupported construct must carry a file and a line.
func TestExternalCronJobSupportReportIsCurrent(t *testing.T) {
	out, err := extractTo(t, filepath.Join(cronJobDir, "project.yaml"))
	if !errors.Is(err, errUnsupported) {
		t.Fatalf("expected extraction to fail closed on unsupported constructs, got %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, supportFile))
	if err != nil {
		t.Fatal(err)
	}
	committed := filepath.Join(cronJobDir, "generated", supportFile)
	want, err := os.ReadFile(committed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; regenerate it with arch --project project.yaml --out generated extract\ngot:\n%s", committed, got)
	}
	if _, err := os.Stat(filepath.Join(cronJobDir, "generated", irFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no IR may be committed while constructs are unsupported: %v", err)
	}
	s := readSupport(t, out)
	if len(s.Unsupported) == 0 {
		t.Fatal("expected unsupported constructs; update FOLLOWUPS.md and commit ir.yaml once the controller extracts")
	}
	for _, u := range s.Unsupported {
		if u.Location.File == "" || u.Location.Line == 0 {
			t.Errorf("unsupported %s %q has no file:line", u.Kind, u.Construct)
		}
	}
}

// TestExternalCronJobAbstraction loads the abstraction document an adopter
// wrote for the pinned controller. Every abstracted resource must be in the
// project's catalog, and the declared controller's key must name an
// abstracted object.
func TestExternalCronJobAbstraction(t *testing.T) {
	p, err := project.Load(filepath.Join(cronJobDir, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	abs, err := abstraction.Load(filepath.Join(p.Dir, p.Spec.Abstraction))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := p.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	objects := map[string]bool{}
	for _, r := range abs.Spec.Resources {
		if _, ok := cat.Lookup(r.GVK); !ok {
			t.Errorf("abstracted resource %s is not in the catalog", r.GVK)
		}
		for _, o := range r.Objects {
			objects[o.ID] = true
		}
	}
	decl, ok := p.Spec.Controller("CronJobReconciler")
	if !ok {
		t.Fatal("project.yaml declares no CronJobReconciler controller")
	}
	if !objects[decl.Key] {
		t.Errorf("controller key %q names no abstracted object", decl.Key)
	}
}

// TestExternalCronJobPipelineReport runs extract, generate, check and
// replay on the pinned controller through the CLI. Extraction fails closed,
// and the later commands refuse to run without an IR. The test turns the
// run into a VerificationReport and compares it with the committed
// report.yaml: the result must be incomplete, every finding unsupported,
// and every construct the extractor reported located. Rewrite the file
// with go test ./cmd/arch -run TestExternalCronJobPipelineReport -update-external.
func TestExternalCronJobPipelineReport(t *testing.T) {
	projectPath := filepath.Join(cronJobDir, "project.yaml")
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--project", projectPath, "--out", out, "extract"}, &stdout, &stderr); !errors.Is(err, errUnsupported) {
		t.Fatalf("arch extract: expected a fail-closed result, got %v", err)
	}
	var findings []report.Finding
	for _, u := range readSupport(t, out).Unsupported {
		if u.Location.File == "" || u.Location.Line == 0 {
			t.Fatalf("unsupported %s %q has no file:line", u.Kind, u.Construct)
		}
		construct, _, _ := strings.Cut(u.Construct, "\n")
		findings = append(findings, report.Finding{Category: report.CategoryUnsupported,
			Message: fmt.Sprintf("arch extract: %s:%d: %s %q: %s", u.Location.File, u.Location.Line, u.Kind, construct, u.Reason)})
	}
	for _, c := range []struct{ command, refusal, finding string }{
		{"generate", irFile + " not found", "arch extract wrote no " + irFile},
		{"check", irFile + " not found", "arch extract wrote no " + irFile},
		{"replay", "no traces match", "arch check sampled no traces"},
	} {
		err := run([]string{"--project", projectPath, "--out", out, c.command}, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), c.refusal) {
			t.Fatalf("arch %s: expected a refusal containing %q, got %v", c.command, c.refusal, err)
		}
		findings = append(findings, report.Finding{Category: report.CategoryUnsupported,
			Message: fmt.Sprintf("arch %s: not run: %s", c.command, c.finding)})
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != supportFile {
		t.Fatalf("the failed pipeline wrote more than %s: %v", supportFile, entries)
	}

	p, err := project.Load(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{}
	for name, sum := range p.Hashes {
		hashes[filepath.ToSlash(name)] = sum
	}
	for _, name := range []string{"internal/controller/cronjob_controller.go", "generated/" + supportFile} {
		b, err := os.ReadFile(filepath.Join(cronJobDir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		hashes[name] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	v := report.Verification{
		Name:        p.Name,
		Mode:        report.ModeInventory,
		Scope:       "no model: arch extract failed closed on the unmodified controller, so arch generate, check and replay did not run; no tool sampled or replayed anything",
		InputHashes: hashes,
		Findings:    findings,
	}
	if got := v.Conclude(); got != report.Incomplete {
		t.Fatalf("pipeline result %s, want %s", got, report.Incomplete)
	}
	gotPath := filepath.Join(out, checkReport)
	if err := v.Write(gotPath); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatal(err)
	}
	committed := filepath.Join(cronJobDir, checkReport)
	if *updateExternal {
		if err := os.WriteFile(committed, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(committed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; rerun with -update-external\ngot:\n%s", committed, got)
	}
}

// generatedDir is the default --out directory.
const generatedDir = "generated"

// copyTree copies a file or a directory tree.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
