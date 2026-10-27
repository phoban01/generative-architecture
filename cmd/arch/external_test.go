package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"generative-architecture/pkg/project"
)

// cronJobDir holds the pinned kubebuilder CronJob tutorial controller
// (Checkpoint B). PIN.md records its source and pin.
const cronJobDir = "../../testdata/external/cronjob"

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
