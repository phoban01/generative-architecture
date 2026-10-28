package main

import (
	"bytes"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/boundary"
	"github.com/phoban01/generative-architecture/pkg/composition"
	"github.com/phoban01/generative-architecture/pkg/contracts"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/project"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// The adoption guides. Each guide test runs every command in the guide's
// sh blocks and fails when the guide lists a command it did not run.
const (
	ownedGuide    = "../../docs/adopt-owned-project.md"
	upstreamGuide = "../../docs/adopt-upstream-project.md"
)

// guideWalk runs guide commands and records them in order.
type guideWalk struct {
	t *testing.T
	// out, when set, holds every --out directory, so a walk never writes
	// into the repository.
	out string
	ran []string
}

// arch runs one arch command line as the guide spells it.
func (w *guideWalk) arch(cmd string) (stdout, stderr string, err error) {
	w.t.Helper()
	w.ran = append(w.ran, cmd)
	args := strings.Fields(cmd)
	if args[0] != "arch" {
		w.t.Fatalf("not an arch command: %s", cmd)
	}
	args = args[1:]
	for i := 0; w.out != "" && i < len(args)-1; i++ {
		if args[i] == "--out" {
			args[i+1] = filepath.Join(w.out, args[i+1])
			i++
		}
	}
	var o, e bytes.Buffer
	err = run(args, &o, &e)
	return o.String(), e.String(), err
}

// shell runs a command line with optional leading NAME=value assignments
// in the current directory.
func (w *guideWalk) shell(cmd string) {
	w.t.Helper()
	w.ran = append(w.ran, cmd)
	fields := strings.Fields(cmd)
	var env []string
	for len(fields) > 0 && strings.Contains(fields[0], "=") {
		env, fields = append(env, fields[0]), fields[1:]
	}
	c := exec.Command(fields[0], fields[1:]...)
	c.Env = append(os.Environ(), env...)
	if out, err := c.CombinedOutput(); err != nil {
		w.t.Fatalf("%s: %v\n%s", cmd, err, out)
	}
}

// skip records a command the walk cannot run, with the reason.
func (w *guideWalk) skip(cmd, reason string) {
	w.t.Helper()
	w.ran = append(w.ran, cmd)
	w.t.Logf("not run: %s: %s", cmd, reason)
}

// matches checks that the walk ran exactly the guide's commands, in order.
func (w *guideWalk) matches(guide string) {
	w.t.Helper()
	if want := guideCommands(w.t, guide); !slices.Equal(w.ran, want) {
		w.t.Fatalf("%s lists commands\n%s\nthe walk ran\n%s", guide, strings.Join(want, "\n"), strings.Join(w.ran, "\n"))
	}
}

// guideCommands returns the command lines of a guide's sh blocks. A line
// that ends in a backslash continues on the next line.
func guideCommands(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cmds []string
	in, cont := false, ""
	for _, line := range strings.Split(string(b), "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case !in && trim == "```sh":
			in = true
		case in && trim == "```":
			in = false
		case in && strings.HasSuffix(trim, `\`):
			cont += strings.TrimSpace(strings.TrimSuffix(trim, `\`)) + " "
		case in && trim != "":
			cmds = append(cmds, cont+trim)
			cont = ""
		}
	}
	return cmds
}

// guideFiles returns the yaml blocks of a guide whose first line is
// "# file: <name>", by name.
func guideFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	re := regexp.MustCompile("(?s)```yaml\n# file: ([^\n]+)\n(.*?)```")
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		files[m[1]] = m[2]
	}
	return files
}

// squeeze collapses runs of white space, so table rows compare without
// their column padding.
func squeeze(s string) string { return strings.Join(strings.Fields(s), " ") }

// absPath resolves a path relative to the test's directory.
func absPath(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestOwnedProjectGuideWalk runs the commands of docs/adopt-owned-project.md
// from the repository root, with every --out directory moved into a
// temporary directory, and checks the messages the guide quotes. It reads
// only the module cache. check and replay need QUINT_BIN, render needs
// LIKEC4_BIN; without them the walk lists those commands as not run.
func TestOwnedProjectGuideWalk(t *testing.T) {
	guide := absPath(t, ownedGuide)
	root := absPath(t, "../..")
	committed, err := project.Load(filepath.Join(refsystemDir, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "-mod=readonly")
	w := &guideWalk{t: t}
	wantErr := func(err error, msg string) {
		t.Helper()
		if err == nil || err.Error() != msg {
			t.Fatalf("got error %v, want %q", err, msg)
		}
	}

	// Section 1.
	w.skip("go install github.com/phoban01/generative-architecture/cmd/arch@v0.1.0", "needs the network")

	// Section 2: arch init in an empty directory writes the module and
	// packages of the committed project, and no catalog or abstraction.
	t.Chdir(t.TempDir())
	if _, _, err := w.arch("arch init --module github.com/phoban01/generative-architecture --package ./examples/refsystem/api/... --package ./examples/refsystem/controllers/tenant --package ./examples/refsystem/controllers/database --package ./examples/refsystem/controllers/backup --name refsystem"); err != nil {
		t.Fatal(err)
	}
	var spec project.Spec
	if _, err := krm.Read(projectFile, project.Kind, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Module != committed.Spec.Module || !slices.Equal(spec.Packages, committed.Spec.Packages) {
		t.Fatalf("arch init wrote module %s packages %v, the committed project has %s %v", spec.Module, spec.Packages, committed.Spec.Module, committed.Spec.Packages)
	}
	if len(spec.Catalog.CRDs)+len(spec.Catalog.Discovery) > 0 || spec.Abstraction != "" || len(spec.Controllers) > 0 {
		t.Fatalf("arch init wrote more than the guide says: %+v", spec)
	}

	// Section 4: inventory extraction matches the committed output: three
	// controllers, seven watches, no unsupported construct and 39
	// unresolved fields.
	t.Chdir(root)
	w.out = t.TempDir()
	refOut := filepath.Join(w.out, "examples/refsystem/generated")
	_, stderr, err := w.arch("arch --project examples/refsystem/project.yaml --out examples/refsystem/generated extract")
	if err != nil || !strings.Contains(stderr, "inventory mode: 39 unsupported or unresolved items in the selected packages; --strict fails on them\n") {
		t.Fatalf("arch extract: %v\n%s", err, stderr)
	}
	for _, name := range []string{irFile, supportFile} {
		got, err := os.ReadFile(filepath.Join(refOut, name))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join("examples/refsystem/generated", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s differs from the committed examples/refsystem/generated/%s", name, name)
		}
	}
	s, err := report.Read(filepath.Join(refOut, supportFile))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, it := range s.Included {
		kinds[it.Kind]++
	}
	if kinds["controller"] != 3 || kinds["watch"] != 7 || len(s.Unsupported) != 0 || len(s.Unresolved) != 39 {
		t.Fatalf("support report: included %v, %d unsupported, %d unresolved", kinds, len(s.Unsupported), len(s.Unresolved))
	}
	out, _, err := w.arch("arch --project examples/refsystem/project.yaml support")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{
		"STATUS KIND CONSTRUCT LOCATION REASON",
		"included controller github.com/phoban01/generative-architecture/examples/refsystem/controllers/backup.BackupReconciler examples/refsystem/controllers/backup/controller.go:34",
		"included reconcile github.com/phoban01/generative-architecture/examples/refsystem/controllers/backup.BackupReconciler examples/refsystem/controllers/backup/controller.go:45 39 process nodes",
		"unresolved field backup.metadata.namespace examples/refsystem/controllers/backup/controller.go:56 no abstraction selects this field",
	} {
		if !strings.Contains(squeeze(out), row) {
			t.Fatalf("arch support lacks %q:\n%s", row, out)
		}
	}

	// Section 5: strict mode fails on the first unresolved field.
	_, _, err = w.arch("arch --strict --project examples/refsystem/project.yaml --out examples/refsystem/generated extract")
	wantErr(err, "arch extract: verification mode (--strict) refuses incomplete semantics: 39 unsupported or unresolved items in the selected packages, first field backup.metadata.namespace at examples/refsystem/controllers/backup/controller.go:56")

	// Section 9: generate refuses three controllers.
	_, _, err = w.arch("arch --project examples/refsystem/project.yaml --out examples/refsystem/generated generate")
	wantErr(err, "arch generate: unsupported operation: "+filepath.Join(refOut, irFile)+" has 3 controllers; composing more than one controller into one model is not supported yet")

	// Section 9: the ConfigMirror pipeline.
	const cm = "arch --project cmd/arch/testdata/configmirror/project.yaml --out /tmp/configmirror "
	for _, c := range []string{"extract", "generate"} {
		if _, _, err := w.arch(cm + c); err != nil {
			t.Fatalf("arch %s: %v", c, err)
		}
	}
	if os.Getenv("QUINT_BIN") == "" {
		w.skip(cm+"check", "QUINT_BIN not set")
		w.skip(cm+"replay", "QUINT_BIN not set")
	} else {
		for _, c := range []struct{ command, want string }{
			{"check", "check passed (inventory mode): 40 traces, 2400 transitions, 0 findings"},
			{"replay", "replay incomplete (inventory mode): 40 traces, 2400 transitions, 1 findings, 1 unsupported"},
		} {
			stdout, stderr, err := w.arch(cm + c.command)
			if err != nil || !strings.Contains(stdout+stderr, c.want) {
				t.Fatalf("arch %s: %v\n%s%s", c.command, err, stdout, stderr)
			}
		}
	}
	if os.Getenv("LIKEC4_BIN") == "" {
		w.skip(cm+"render", "LIKEC4_BIN not set")
	} else if stdout, stderr, err := w.arch(cm + "render"); err != nil || !strings.HasPrefix(stdout+stderr, "rendered ") {
		t.Fatalf("arch render: %v\n%s%s", err, stdout, stderr)
	}

	w.matches(guide)
}

// TestUpstreamProjectGuideWalk runs the commands of
// docs/adopt-upstream-project.md on a copy of the pinned CronJob
// controller, from the directory an adopter works in, and checks the
// messages the guide quotes. It reads only the module cache.
func TestUpstreamProjectGuideWalk(t *testing.T) {
	guide := absPath(t, upstreamGuide)
	src := absPath(t, cronJobDir)
	dir := t.TempDir()
	for _, name := range []string{"go.mod", "go.sum", "upstream.sha256", "LICENSE", "PROJECT", "api", "internal", "config", "catalog"} {
		copyTree(t, filepath.Join(src, name), filepath.Join(dir, name))
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "-mod=readonly")
	t.Chdir(dir)
	w := &guideWalk{t: t}
	wantErr := func(err error, msg string) {
		t.Helper()
		if err == nil || err.Error() != msg {
			t.Fatalf("got error %v, want %q", err, msg)
		}
	}

	// Section 1: the copy matches the pin and builds from the cache.
	const sumCheck = "sha256sum -c upstream.sha256"
	if _, err := exec.LookPath("sha256sum"); err != nil {
		w.skip(sumCheck, "sha256sum not on PATH; TestExternalCronJobPinned checks the hashes")
	} else {
		w.shell(sumCheck)
	}
	w.shell("GOWORK=off go build ./...")

	// Section 2: arch init writes no catalog, abstraction or controllers.
	if _, _, err := w.arch("arch init --module tutorial.kubebuilder.io/project --package ./internal/controller --package ./api/v1 --name cronjob"); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	if s := p.Spec; len(s.Catalog.CRDs)+len(s.Catalog.Discovery) > 0 || s.Abstraction != "" || len(s.Controllers) > 0 {
		t.Fatalf("arch init wrote more than the guide says: %+v", s)
	}

	// Section 4: without a catalog and an abstraction, extraction reads
	// SetupWithManager and Reconcile and lists every gap, including the
	// setup preamble and the first Reconcile statement.
	_, _, err = w.arch("arch extract")
	wantErr(err, "arch extract: unsupported constructs: 54 found, listed in generated/support.yaml")
	s, err := report.Read(filepath.Join(generatedDir, supportFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Unsupported) != 54 || len(s.Unresolved) != 1 || s.Unresolved[0].Construct != "cronJob.spec.concurrencyPolicy" {
		t.Fatalf("report without a catalog: %d unsupported, unresolved %+v", len(s.Unsupported), s.Unresolved)
	}
	if _, err := os.Stat(filepath.Join(generatedDir, irFile)); !os.IsNotExist(err) {
		t.Fatalf("arch extract wrote an IR next to unsupported constructs: %v", err)
	}
	out, stderr, err := w.arch("arch support")
	for _, row := range []string{
		"internal/controller/cronjob_controller.go:553  expected an unstructured object declaration",
		"internal/controller/cronjob_controller.go:101  unsummarized call to sigs.k8s.io/controller-runtime/pkg/log.FromContext",
	} {
		if err != nil || !strings.Contains(out, row) {
			t.Fatalf("arch support lacks %q: %v\n%s", row, err, out)
		}
	}
	if !strings.Contains(stderr, "inventory mode: 55 unsupported or unresolved items in the selected packages; --strict fails on them") {
		t.Fatalf("arch support: %s", stderr)
	}

	// Section 5: strict mode fails on the first construct in file order.
	_, _, err = w.arch("arch --strict support")
	wantErr(err, "arch support: verification mode (--strict) refuses incomplete semantics: 55 unsupported or unresolved items in the selected packages, first call log := logf.FromContext(ctx) at internal/controller/cronjob_controller.go:101")

	// Section 6 with the committed project and abstraction. The
	// abstraction selects spec.suspend and status.lastScheduleTime, so
	// the frontend reads further on their lines.
	for _, name := range []string{projectFile, "abstraction.yaml"} {
		copyTree(t, filepath.Join(src, name), filepath.Join(dir, name))
	}
	_, _, err = w.arch("arch --strict extract")
	wantErr(err, "arch extract: unsupported constructs: 55 found, listed in generated/support.yaml")
	got, err := os.ReadFile(filepath.Join(generatedDir, supportFile))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(src, generatedDir, supportFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the report with the committed project differs from the committed one:\n%s", got)
	}
	for _, c := range []struct{ command, msg string }{
		{"arch generate", "arch generate: generated/ir.yaml not found; run arch extract first"},
		{"arch check", "arch check: generated/ir.yaml not found; run arch extract and arch generate first"},
		{"arch replay", "arch replay: no traces match generated/traces/*.itf.json; run arch check first or pass --traces"},
		{"arch render", "arch render: no workspace in generated/likec4; run arch generate first"},
	} {
		_, _, err = w.arch(c.command)
		wantErr(err, c.msg)
	}

	w.matches(guide)
}

// TestUpstreamProjectGuideBoundary checks the boundary, contract and
// composition sections of docs/adopt-upstream-project.md: the library
// documents its tables list load with the versions shown, the external
// selector it shows checks a provider type, and the system.yaml and
// consumer.yaml it shows compose with the Flux boundary.
func TestUpstreamProjectGuideBoundary(t *testing.T) {
	b, err := os.ReadFile(upstreamGuide)
	if err != nil {
		t.Fatal(err)
	}
	guide := string(b)
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPROXY", "off")

	// Section 7: the boundary library table.
	rows := regexp.MustCompile("(?m)^\\| `(pkg/boundary/library/[^`]+)` \\| .* (v[0-9.]+) [^|]*\\|$").FindAllStringSubmatch(guide, -1)
	if len(rows) != 4 {
		t.Fatalf("the guide lists %d library boundaries, want 4", len(rows))
	}
	for _, row := range rows {
		bm, err := boundary.Load(filepath.Join("../..", row[1]))
		if err != nil {
			t.Fatal(err)
		}
		if bm.Spec.Version != row[2] || bm.Model == nil {
			t.Errorf("%s: version %s, want %s; model loaded: %v", row[1], bm.Spec.Version, row[2], bm.Model != nil)
		}
	}

	// Section 8: the contract library table and the external selector.
	const capiDir = "../../pkg/contracts/library/clusterapi"
	rows = regexp.MustCompile("(?m)^\\| `([a-z0-9-]+\\.yaml)` \\| `([^`]+)` \\|$").FindAllStringSubmatch(guide, -1)
	if len(rows) != 4 {
		t.Fatalf("the guide lists %d contracts, want 4", len(rows))
	}
	var defs []contracts.Definition
	for _, row := range rows {
		d, err := contracts.Load(filepath.Join(capiDir, row[1]))
		if err != nil {
			t.Fatal(err)
		}
		if got := d.Name + "/" + d.Version; got != row[2] {
			t.Errorf("%s is contract %s, the guide says %s", row[1], got, row[2])
		}
		defs = append(defs, d)
	}
	provider := absPath(t, "../../pkg/contracts/testdata/capiprovider")
	root := t.TempDir()
	mod := "module example.org/consumer\n\ngo 1.25\n\nrequire example.org/capiprovider v0.0.0\n\nreplace example.org/capiprovider => " + provider + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	evidence, err := contracts.Inspect(root, defs, nil, []contracts.Implementation{{
		Contract: "capi.infrastructure-machine/v1beta2",
		Package:  "example.org/capiprovider",
		Type:     "InfraMachineV1Beta2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || evidence[0].JSONPaths["status.initialization.provisioned"] != "status.initialization.provisioned" || evidence[0].Module != "example.org/capiprovider" {
		t.Fatalf("evidence %+v", evidence)
	}

	// Section 9: compose the guide's documents with the Flux boundary.
	files := guideFiles(t, upstreamGuide)
	if len(files) != 2 || files["system.yaml"] == "" || files["consumer.yaml"] == "" {
		t.Fatalf("the guide has files %v, want system.yaml and consumer.yaml", slices.Sorted(maps.Keys(files)))
	}
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "boundaries", "flux"), os.DirFS("../../pkg/boundary/library/flux")); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := composition.Load(dir, "system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Components[0].Evidence != ir.Assumed {
		t.Fatalf("flux component evidence %q, want %q", loaded.Components[0].Evidence, ir.Assumed)
	}
	m, err := model.Compile(loaded.Result.Model)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Next(state, "consumer_reconcile"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("consumer_reconcile before the apply: got %v, want a disabled action", err)
	}
	for _, event := range []string{"flux_dependenciesReady", "flux_apply", "flux_markReady", "consumer_reconcile"} {
		if state, err = m.Next(state, event); err != nil {
			t.Fatalf("%s: %v", event, err)
		}
		if err = m.Check(state, event); err != nil {
			t.Fatalf("%s: %v", event, err)
		}
	}
	if state["consumer_reconciled"] != true {
		t.Fatalf("consumer did not reconcile: %v", state)
	}
}

// TestReleaseChecklistReferences checks that every repository path in
// backticks, every make target and every ADR that docs/release.md names
// exists, so the checklist cannot point at a moved or deleted file.
func TestReleaseChecklistReferences(t *testing.T) {
	const root = "../.."
	b, err := os.ReadFile(filepath.Join(root, "docs", "release.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	n := 0
	pathLike := regexp.MustCompile(`^\.?[\w-]+(\.[\w-]+)*(/[\w.-]+)*/?$`)
	fileExt := []string{".md", ".mod", ".sum", ".lock", ".json", ".yaml", ".yml"}
	for _, m := range regexp.MustCompile("`([^`\\s]+)`").FindAllStringSubmatch(text, -1) {
		tok := m[1]
		first, _, nested := strings.Cut(tok, "/")
		switch {
		case !pathLike.MatchString(tok):
			continue
		case nested && strings.Contains(first, ".") && !strings.HasPrefix(first, "."):
			continue // a module path or an API group, such as github.com/...
		case !nested && !slices.Contains(fileExt, filepath.Ext(tok)):
			continue // a name, a version or a flag, not a file
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(tok))); err != nil {
			t.Errorf("docs/release.md names %s: %v", tok, err)
		}
		n++
	}
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`\bmake ([a-z][\w-]*)`).FindAllStringSubmatch(text, -1) {
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(m[1]) + `:`).Match(makefile) {
			t.Errorf("docs/release.md names make %s, which the Makefile does not define", m[1])
		}
		n++
	}
	for _, m := range regexp.MustCompile(`ADR-(\d{4})`).FindAllStringSubmatch(text, -1) {
		if files, _ := filepath.Glob(filepath.Join(root, "docs", "adr", m[1]+"-*.md")); len(files) != 1 {
			t.Errorf("docs/release.md names ADR-%s, found %v", m[1], files)
		}
		n++
	}
	if n < 20 {
		t.Fatalf("found only %d references in docs/release.md; the parser misses them", n)
	}
}
