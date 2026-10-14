package mutation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const suiteDoc = `apiVersion: architecture.local/v1alpha1
kind: MutationSuite
metadata:
  name: counter
spec:
  mutations:
    - name: skip-increment
      file: counter.txt
      find: "count = count + 1"
      replace: "count = count"
      expect: [progress]
    - name: double-increment
      file: counter.txt
      find: "count = count + 1"
      replace: "count = count + 2"
      expect: [bounded, progress]
`

func writeSuite(t *testing.T, doc string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "counter.txt"), []byte("count = 0\ncount = count + 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "suite.yaml")
	if err := os.WriteFile(path, []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// evaluate is a stand-in checker: it reads the mutated program text and
// reports which constraints the final count violates.
func evaluate(program string) []string {
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(program), "\n") {
		switch line {
		case "count = count + 1":
			count++
		case "count = count + 2":
			count += 2
		}
	}
	var violated []string
	if count == 0 {
		violated = append(violated, "progress")
	}
	if count > 1 {
		violated = append(violated, "bounded")
	}
	return violated
}

func TestApplyInMemory(t *testing.T) {
	path := writeSuite(t, suiteDoc)
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "counter" || s.Root != filepath.Dir(path) || len(s.Spec.Mutations) != 2 {
		t.Fatalf("unexpected suite %+v", s)
	}
	out, err := Apply(s.Root, s.Spec.Mutations[1])
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "count = 0\ncount = count + 2\n" {
		t.Fatalf("mutated %q", out)
	}
	original, err := os.ReadFile(filepath.Join(s.Root, "counter.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != "count = 0\ncount = count + 1\n" {
		t.Fatalf("Apply wrote to disk: %q", original)
	}
	if _, err = Apply(s.Root, Mutation{Name: "absent", File: "counter.txt", Find: "nothing here"}); err == nil || !strings.Contains(err.Error(), "does not occur") {
		t.Fatalf("no-op mutation accepted: %v", err)
	}
}

func TestRunDetectionMatrix(t *testing.T) {
	s, err := Load(writeSuite(t, suiteDoc))
	if err != nil {
		t.Fatal(err)
	}
	var current string
	build := func(mutated map[string][]byte) error {
		b, ok := mutated["counter.txt"]
		if !ok || len(mutated) != 1 {
			return fmt.Errorf("unexpected files %v", mutated)
		}
		current = string(b)
		return nil
	}
	matrix, err := Run(s, build, func() []string { return evaluate(current) })
	if err != nil {
		t.Fatal(err)
	}
	want := "double-increment: +bounded -progress\nskip-increment: +progress\n"
	if matrix.String() != want {
		t.Fatalf("matrix:\n%s\nwant:\n%s", matrix, want)
	}
	if escaped := s.Escaped(matrix); len(escaped) != 1 || escaped[0] != "double-increment: progress" {
		t.Fatalf("escaped %v", escaped)
	}
	if uncovered := s.Uncovered([]string{"progress", "bounded", "monotonic"}); len(uncovered) != 1 || uncovered[0] != "monotonic" {
		t.Fatalf("uncovered %v", uncovered)
	}
	_, err = Run(s, func(map[string][]byte) error { return fmt.Errorf("syntax error") }, func() []string { return nil })
	if err == nil || !strings.Contains(err.Error(), "skip-increment: build: syntax error") {
		t.Fatalf("build failure not reported: %v", err)
	}
}

func TestLoadFailures(t *testing.T) {
	cases := map[string]struct{ doc, want string }{
		"duplicate name":   {strings.Replace(suiteDoc, "name: double-increment", "name: skip-increment", 1), "twice"},
		"bad name":         {strings.Replace(suiteDoc, "name: skip-increment", "name: 1st", 1), "identifier"},
		"escaping file":    {strings.Replace(suiteDoc, "file: counter.txt", "file: ../counter.txt", 1), "relative"},
		"empty find":       {strings.Replace(suiteDoc, `find: "count = count + 1"`, `find: ""`, 1), "find must not be empty"},
		"no-op":            {strings.Replace(suiteDoc, `replace: "count = count"`, `replace: "count = count + 1"`, 1), "replace equals find"},
		"no expectations":  {strings.Replace(suiteDoc, "expect: [progress]", "expect: []", 1), "expect at least one"},
		"duplicate expect": {strings.Replace(suiteDoc, "expect: [bounded, progress]", "expect: [bounded, bounded]", 1), "duplicate"},
		"unknown field":    {strings.Replace(suiteDoc, "expect: [progress]", "expect: [progress]\n      severity: high", 1), "severity"},
		"wrong kind":       {strings.Replace(suiteDoc, "kind: MutationSuite", "kind: Project", 1), "expected"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeSuite(t, tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}
