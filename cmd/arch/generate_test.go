package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// configMirror is the ConfigMirror sample with its model declarations in
// the project document.
const configMirror = "../../examples/configmirror/project.yaml"

// generateTo runs arch extract and arch generate into a temporary
// directory and returns the directory and generate's error.
func generateTo(t *testing.T, projectPath string) (string, error) {
	t.Helper()
	out, err := extractTo(t, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	return out, run([]string{"--project", projectPath, "--out", out, "generate"}, &stdout, &stderr)
}

func TestGenerateConfigMirror(t *testing.T) {
	out, err := generateTo(t, configMirror)
	if err != nil {
		t.Fatal(err)
	}
	// The model equals the one examples/configmirror/generate.go builds by
	// hand from the same inputs.
	for file, want := range map[string]string{modelFile: "Model.qnt", specFile: "model.yaml"} {
		got, err := os.ReadFile(filepath.Join(out, file))
		if err != nil {
			t.Fatal(err)
		}
		golden, err := os.ReadFile(filepath.Join("../../examples/configmirror/generated", want))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, golden) {
			t.Errorf("%s differs from the hand-assembled %s", file, want)
		}
	}
	for _, view := range []string{"context", "process", "resources", "runtime"} {
		if _, err := os.Stat(filepath.Join(out, likec4Dir, "views", view+".c4")); err != nil {
			t.Errorf("view %s: %v", view, err)
		}
	}
	r, err := readReceipt(filepath.Join(out, receiptsFile))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, irFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Generated) != 6 || r.Generated[0].Path != irFile || r.Generated[0].SHA256 != fmt.Sprintf("%x", sha256.Sum256(b)) || r.Generated[3].Path != "adapter/main_generated.go" {
		t.Fatalf("receipt does not record ir.yaml and the adapter: %+v", r.Generated)
	}
	recorded := map[string]bool{}
	for _, f := range r.Files {
		recorded[f.Path] = true
	}
	for _, want := range []string{"examples/configmirror/controller.go", "examples/configmirror/project.yaml", "examples/configmirror/abstraction.yaml", "examples/configmirror/constraints.yaml", "examples/configmirror/hints.yaml", "go.mod"} {
		if !recorded[want] {
			t.Errorf("receipt does not record %s", want)
		}
	}
	if !r.Owns("examples/configmirror/controller.go") || !r.Owns("examples/configmirror/project.yaml") || r.Owns("pkg/model/model.go") || r.Owns("go.mod") {
		t.Errorf("receipt owned entries %v", r.Owned)
	}
	if r.Bounds["maxInvocations"] != 3 || r.Tools["go"] == "" {
		t.Errorf("receipt bounds %v tools %v", r.Bounds, r.Tools)
	}
	likec4Validate(t, filepath.Join(out, likec4Dir))
}

func TestGenerateWithoutControllers(t *testing.T) {
	out, err := generateTo(t, "testdata/apionly/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, modelFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a project without controllers has no model: %v", err)
	}
	r, err := readReceipt(filepath.Join(out, receiptsFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Generated) != 1 || r.Generated[0].Path != irFile {
		t.Fatalf("receipt does not record ir.yaml and the adapter: %+v", r.Generated)
	}
	likec4Validate(t, filepath.Join(out, likec4Dir))
}

func TestGenerateFailsClosed(t *testing.T) {
	t.Run("no IR", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run([]string{"--project", configMirror, "--out", t.TempDir(), "generate"}, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "run arch extract first") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("extract failed closed", func(t *testing.T) {
		out := t.TempDir()
		support := report.Support{Unsupported: []report.Item{
			{Kind: "statement", Construct: "for range jobs", Reason: "range loops are not summarized", Location: report.Location{File: "controller.go", Line: 163}},
			{Kind: "call", Construct: "len(active)", Location: report.Location{File: "controller.go", Line: 170}},
		}}
		if err := support.Write(filepath.Join(out, supportFile)); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		err := run([]string{"--project", configMirror, "--out", out, "generate"}, &stdout, &stderr)
		want := "arch extract failed closed on 2 unsupported constructs, listed in " + filepath.Join(out, supportFile) + "; first: statement for range jobs at controller.go:163"
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "run arch extract first") {
			t.Fatalf("got %v, want %q", err, want)
		}
	})
	t.Run("no declaration", func(t *testing.T) {
		_, err := generateTo(t, "testdata/configmirror/nodecl.yaml")
		if err == nil || !strings.Contains(err.Error(), "has no declaration") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("undeclared client call", func(t *testing.T) {
		out, err := generateTo(t, "testdata/configmirror/undeclared.yaml")
		d, ok := goflow.AsDiagnostic(err)
		if !ok || d.File != "examples/configmirror/controller.go" || d.Line != 30 || !strings.Contains(d.Error(), "client.Client.Update") {
			t.Fatalf("expected a located diagnostic at the Update call, got %v", err)
		}
		if _, err := os.Stat(filepath.Join(out, modelFile)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("no model may be written: %v", err)
		}
	})
}

// likec4Validate runs `likec4 validate` on a workspace directory when
// LIKEC4_BIN is set.
func likec4Validate(t *testing.T, dir string) {
	t.Helper()
	bin := os.Getenv("LIKEC4_BIN")
	if bin == "" {
		t.Log("LIKEC4_BIN not set; skipping LikeC4 validation")
		return
	}
	out, err := exec.Command(bin, "validate", dir).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Valid") {
		t.Fatalf("likec4 validate: %v\n%s", err, out)
	}
}

// A return that asks for a delayed requeue reaches the generated model: the
// process reports the after outcome and the runtime schedules the key.
func TestGenerateCarriesRequeue(t *testing.T) {
	out, err := generateTo(t, "testdata/requeue/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	if v, ok := spec.State[outcomeVariable]; !ok || v.Type != "string" {
		t.Fatalf("model has no string %s variable: %+v", outcomeVariable, spec.State[outcomeVariable])
	}
	var after, reportsAfter bool
	for _, a := range spec.Actions {
		if a.Name == eventFinishAfter {
			after = true
		}
		if strings.Contains(string(a.Updates[outcomeVariable]), `"after"`) {
			reportsAfter = true
		}
	}
	if !after || !reportsAfter {
		t.Fatalf("after=%v reportsAfter=%v: the requeue did not reach the model", after, reportsAfter)
	}
}

// The project document's viewHints are applied to the generated workspace:
// the context view carries the hinted title and layout.
func TestGenerateAppliesViewHints(t *testing.T) {
	out, err := generateTo(t, configMirror)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, likec4Dir, "views", "context.c4"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"title 'ConfigMirror in context'", "autoLayout LeftRight"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("views lack %q:\n%s", want, b)
		}
	}
	r, err := readReceipt(filepath.Join(out, receiptsFile))
	if err != nil {
		t.Fatal(err)
	}
	var recorded bool
	for _, f := range r.Files {
		if strings.HasSuffix(f.Path, "hints.yaml") {
			recorded = true
		}
	}
	if !recorded {
		t.Errorf("the receipt does not record the hints document: %+v", r.Files)
	}
}

// A temporal property over an unknown symbol fails arch generate, not only
// arch check, so a bad constraints document never reaches sampling.
func TestGenerateValidatesTemporal(t *testing.T) {
	_, err := generateTo(t, "testdata/configmirror/bad-temporal.yaml")
	want := "bad-temporal-constraints.yaml: temporal reachesMissing: eventually: line 1, column 1: unknown CEL symbol missing"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %q", err, want)
	}
}

// The generated model declares one domain per resource with the IDs of its
// declared objects, so a constraint can quantify over the population.
func TestGenerateDeclaresPopulations(t *testing.T) {
	out, err := generateTo(t, configMirror)
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	if got := spec.Domains["ConfigMap_objects"]; !reflect.DeepEqual(got, []string{"source", "copy"}) {
		t.Fatalf("ConfigMap_objects = %v, want the declared object IDs", got)
	}
	spec.Properties = append(spec.Properties, model.Property{Name: "namedObjects", Expression: `ConfigMap_objects.all(o, o != "")`})
	if _, err := model.Compile(spec); err != nil {
		t.Fatalf("a quantifier over the population does not compile: %v", err)
	}
}

// A selected field under a quoted map key generates a model whose variables
// use the field's name, and the Go read and write of the key match it.
func TestGenerateQuotedMapKey(t *testing.T) {
	out, err := generateTo(t, "testdata/keyed/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ConfigMap_source_appKey", "ConfigMap_copy_appKey"} {
		if _, ok := spec.State[name]; !ok {
			t.Errorf("model lacks %s: %v", name, slices.Sorted(maps.Keys(spec.State)))
		}
	}
	for name := range spec.State {
		if strings.Contains(name, "app.config") {
			t.Errorf("raw map key in a variable name: %s", name)
		}
	}
	var copied bool
	for _, a := range spec.Actions {
		if a.Updates["target_appKey"] == "source_appKey" {
			copied = true
		}
	}
	if !copied {
		t.Fatalf("the keyed value is not copied between the locals: %+v", spec.Actions)
	}
}

// arch generate resolves a Go API type by the group and version its package
// declares, as arch extract does, and fails closed on a package that
// declares neither.
func TestGenerateResolvesGroupVersion(t *testing.T) {
	out, err := generateTo(t, "testdata/grouped/widgetgen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.State["Widget_w1_metadata_labels_tier"]; !ok {
		t.Fatalf("model lacks the Widget field: %v", slices.Sorted(maps.Keys(spec.State)))
	}
	_, err = generateTo(t, "testdata/grouped/gadgetgen.yaml")
	want := "cmd/arch/testdata/grouped/gadgetgen/controller.go:19: package github.com/phoban01/generative-architecture/cmd/arch/testdata/grouped/api/v1beta1 declares no GroupVersion variable and no +groupName marker"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %q", err, want)
	}
}

// A resource with several selected fields generates one variable per
// field: a read fills every field's local, a write updates every field's
// variable, and the environment changes each field under its own event.
func TestGenerateSeveralFields(t *testing.T) {
	out, err := generateTo(t, "testdata/fields/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ConfigMap_source_data_value", "ConfigMap_source_data_mode", "ConfigMap_copy_data_value", "ConfigMap_copy_data_mode", "source_data_value", "source_data_mode", "target_data_value", "target_data_mode"} {
		if _, ok := spec.State[name]; !ok {
			t.Errorf("model lacks %s: %v", name, slices.Sorted(maps.Keys(spec.State)))
		}
	}
	if spec.State["ConfigMap_source_data_value"].Initial != `"a"` || spec.State["ConfigMap_copy_data_mode"].Initial != `""` {
		t.Errorf("initial values not taken per field: %+v", spec.State)
	}
	actions := map[string]model.Action{}
	for _, a := range spec.Actions {
		actions[a.Name] = a
	}
	write := actions["writeTarget_ok"]
	if write.Updates["ConfigMap_copy_data_value"] != "target_data_value" || write.Updates["ConfigMap_copy_data_mode"] != "target_data_mode" {
		t.Errorf("the update does not write both fields: %+v", write)
	}
	read := actions["readSource_found"]
	if read.Updates["source_data_value"] != "cached_ConfigMap_source_data_value" || read.Updates["source_data_mode"] != "cached_ConfigMap_source_data_mode" {
		t.Errorf("the read does not fill both locals: %+v", read)
	}
	for _, name := range []string{"changeSource_data_value_a", "changeSource_data_mode_fast"} {
		if _, ok := actions[name]; !ok {
			t.Errorf("no environment action %s: %v", name, slices.Sorted(maps.Keys(actions)))
		}
	}
}

// Two declared controllers compose into one model: the abstraction's state
// exists once under its own names, each controller's process and runtime
// state carries its name, the environment actions over shared state exist
// once, the copy's ConfigMap port binds Exporter to Mirror, and the
// workspace draws the binding with one process view per controller.
func TestGenerateComposesControllers(t *testing.T) {
	out, err := generateTo(t, "testdata/pair/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ConfigMap_source_data_value", "ConfigMap_copy_data_value", "Secret_export_stringData_value", "mirror_pc", "exporter_pc", "mirror_source_data_value", "exporter_copied_data_value", "mirror_cached_ConfigMap_source_data_value"} {
		if _, ok := spec.State[name]; !ok {
			t.Errorf("composed model lacks %s: %v", name, slices.Sorted(maps.Keys(spec.State)))
		}
	}
	actions := map[string]model.Action{}
	for _, a := range spec.Actions {
		actions[a.Name] = a
	}
	for _, name := range []string{"mirror_writeTarget_ok", "exporter_writeSecret_ok", "mirror_begin", "exporter_begin", "changeSource_a", "mirror_deliverCache", "exporter_deliverCache"} {
		if _, ok := actions[name]; !ok {
			t.Errorf("composed model lacks action %s: %v", name, slices.Sorted(maps.Keys(actions)))
		}
	}
	if _, ok := actions["changeSource_a"]; ok {
		if c := 0; c == 0 {
			for name := range actions {
				if strings.HasSuffix(name, "changeSource_a") {
					c++
				}
			}
			if c != 1 {
				t.Errorf("the environment action exists %d times", c)
			}
		}
	}
	if actions["exporter_writeSecret_ok"].Updates["Secret_export_stringData_value"] != "exporter_secret_stringData_value" {
		t.Errorf("the Exporter write does not reach the shared Secret state: %+v", actions["exporter_writeSecret_ok"])
	}
	for _, f := range []string{"process-mirror.yaml", "process-exporter.yaml"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("process document: %v", err)
		}
	}
	views, err := os.ReadFile(filepath.Join(out, likec4Dir, "model.c4"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(views), "component_mirror -> component_exporter") {
		t.Errorf("context view lacks the binding from mirror to exporter:\n%s", views)
	}
	for _, f := range []string{"views/process_mirror.c4", "views/process_exporter.c4", "views/runtime_mirror.c4", "views/runtime_exporter.c4"} {
		if _, err := os.Stat(filepath.Join(out, likec4Dir, f)); err != nil {
			t.Errorf("view: %v", err)
		}
	}
}

// A composed model's bindings reach the check report: the copy's ConfigMap
// port of the exporter is bound to the mirror's. The composition document
// is a generated input of the run.
func TestCheckReportsBindings(t *testing.T) {
	if os.Getenv("QUINT_BIN") == "" {
		t.Skip("QUINT_BIN not set")
	}
	const pair = "testdata/pair/project.yaml"
	out, err := generateTo(t, pair)
	if err != nil {
		t.Fatal(err)
	}
	r, err := readReceipt(filepath.Join(out, receiptsFile))
	if err != nil {
		t.Fatal(err)
	}
	var recorded bool
	for _, f := range r.Generated {
		recorded = recorded || f.Path == compositionFile
	}
	if !recorded {
		t.Fatalf("the receipt does not record %s: %+v", compositionFile, r.Generated)
	}
	if _, err := arch(t, pair, out, false, "check", "--samples", "4", "--steps", "20"); err != nil {
		t.Fatal(err)
	}
	v := readVerification(t, filepath.Join(out, checkReport))
	if len(v.Bindings) != 1 || v.Bindings[0].Link != "mirror.configmap_v1 -> exporter.configmap_v1" || v.Bindings[0].Status != report.BindingBound {
		t.Fatalf("bindings %+v", v.Bindings)
	}
	if len(v.Protocols) != 0 {
		t.Fatalf("protocols %+v", v.Protocols)
	}
}

// A Create effect generates from the built-in create summary: the object
// starts absent when spec.initial says so, and the created outcome sets
// its existence and value.
func TestGenerateCreatesObject(t *testing.T) {
	out, err := generateTo(t, "testdata/create/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	if spec.State["ConfigMap_copy_exists"].Initial != "false" {
		t.Fatalf("the created object does not start absent: %+v", spec.State["ConfigMap_copy_exists"])
	}
	var created *model.Action
	for i := range spec.Actions {
		if spec.Actions[i].Name == "createCopy_created" {
			created = &spec.Actions[i]
		}
	}
	if created == nil || created.Updates["ConfigMap_copy_exists"] != "true" || created.Updates["ConfigMap_copy_data_value"] != "cm_data_value" {
		t.Fatalf("created outcome %+v", created)
	}
}

// A Status().Update effect generates from the status.update summary: the
// ok outcome replaces the selected status field with the local's value
// and a stale copy conflicts.
func TestGenerateStatusUpdate(t *testing.T) {
	out, err := generateTo(t, "testdata/status/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	actions := map[string]model.Action{}
	for _, a := range spec.Actions {
		actions[a.Name] = a
	}
	ok := actions["writeStatus_ok"]
	if ok.Updates["Namespace_demo_status_phase"] != "ns_status_phase" || !strings.Contains(string(ok.Guard), "Namespace_demo_metadata_resourceVersion == ns_metadata_resourceVersion") {
		t.Fatalf("status update %+v", ok)
	}
	if _, conflict := actions["writeStatus_conflict"]; !conflict {
		t.Fatalf("no conflict outcome: %v", slices.Sorted(maps.Keys(actions)))
	}
	// The effect is an update of the status subresource in the resource view.
	c4, err := os.ReadFile(filepath.Join(out, likec4Dir, "model.c4"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c4), "writeStatus: status.update (direct)") {
		t.Fatalf("the resource view does not record the status subresource:\n%s", c4)
	}
}

// A read keyed by a client.ObjectKey literal built from another object's
// name addresses the object with that name.
func TestGenerateResolvesObjectKeyLiteral(t *testing.T) {
	out, err := generateTo(t, "testdata/named/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	guards := map[string]string{}
	for _, a := range spec.Actions {
		guards[a.Name] = string(a.Guard)
	}
	for action, exists := range map[string]string{"readConfigMap_found": "ConfigMap_source_exists", "readNamespace_notFound": "!Namespace_acme_exists"} {
		if !strings.Contains(guards[action], exists) {
			t.Fatalf("%s guards %q, want %s: %v", action, guards[action], exists, slices.Sorted(maps.Keys(guards)))
		}
	}
}

// A Status().Patch with client.Merge generates from the status.patch
// summary: the ok outcome replaces the status field and a stale copy
// conflicts, as the patch carries the object's version.
func TestGenerateStatusPatch(t *testing.T) {
	out, err := generateTo(t, "testdata/patch/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	actions := map[string]model.Action{}
	for _, a := range spec.Actions {
		actions[a.Name] = a
	}
	ok := actions["writeStatus_ok"]
	if ok.Updates["Namespace_demo_status_phase"] != "ns_status_phase" || !strings.Contains(string(ok.Guard), "Namespace_demo_metadata_resourceVersion == ns_metadata_resourceVersion") {
		t.Fatalf("status patch %+v", ok)
	}
	if _, conflict := actions["writeStatus_conflict"]; !conflict {
		t.Fatalf("no conflict outcome: %v", slices.Sorted(maps.Keys(actions)))
	}
	c4, err := os.ReadFile(filepath.Join(out, likec4Dir, "model.c4"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c4), "writeStatus: status.patch (direct)") {
		t.Fatalf("the resource view does not record the status patch:\n%s", c4)
	}
}

// The reference system composes: its three controllers and the Flux
// boundary generate into one
// model. The derived bindings join the tenant controller's Tenant to
// the database controller, the database controller's Database to the
// backup controller, and each owned kind to the Kustomization that applies
// it.
func TestGenerateReferenceSystem(t *testing.T) {
	out, err := generateTo(t, filepath.Join(refsystemDir, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc compositionDoc
	if err := krm.ReadResult(filepath.Join(out, compositionFile), compositionKind, &doc); err != nil {
		t.Fatal(err)
	}
	if want := []string{"backup", "database", "tenant", "flux"}; !slices.Equal(doc.Components, want) {
		t.Fatalf("components %v, want %v", doc.Components, want)
	}
	var links []string
	for _, l := range doc.Links {
		links = append(links, l.From+" -> "+l.To)
	}
	want := []string{
		"database.database_v1alpha1_refsystem_example_org -> backup.database_v1alpha1_refsystem_example_org",
		"tenant.tenant_v1alpha1_refsystem_example_org -> database.tenant_v1alpha1_refsystem_example_org",
		"backup.backup_v1alpha1_refsystem_example_org -> flux.backup_v1alpha1_refsystem_example_org",
		"database.database_v1alpha1_refsystem_example_org -> flux.database_v1alpha1_refsystem_example_org",
		"tenant.tenant_v1alpha1_refsystem_example_org -> flux.tenant_v1alpha1_refsystem_example_org",
	}
	if !slices.Equal(links, want) {
		t.Fatalf("links %v, want %v", links, want)
	}
	for _, view := range []string{"context", "process_tenant", "process_database", "process_backup", "runtime_tenant"} {
		if _, err := os.Stat(filepath.Join(out, likec4Dir, "views", view+".c4")); err != nil {
			t.Fatalf("view %s: %v", view, err)
		}
	}
}

// A range over a list local the abstraction declares generates a loop: the
// index the frontend introduces is a process local, the head tests it
// against the list's size, and the model type-checks in Quint (ADR-0011).
func TestGenerateRangeLoop(t *testing.T) {
	out, err := generateTo(t, "testdata/loops/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	if v := spec.State["active"]; v.Type != "list(string)" || v.Initial != "[]" {
		t.Fatalf("list local %+v", v)
	}
	if v := spec.State["id_index"]; v.Type != "int" || v.Initial != "0" {
		t.Fatalf("the loop index is not a process local: %+v; state %v", v, slices.Sorted(maps.Keys(spec.State)))
	}
	var appended, head, bound bool
	for _, a := range spec.Actions {
		if strings.Contains(string(a.Updates["active"]), `active + ["source"]`) {
			appended = true
		}
		if strings.Contains(string(a.Updates[pcVariable]), "(id_index < size(active))") {
			head = true
		}
		if a.Updates["id"] == "active[id_index]" {
			bound = true
		}
	}
	if !appended || !head || !bound {
		t.Fatalf("appended=%v head=%v bound=%v", appended, head, bound)
	}
	quint := os.Getenv("QUINT_BIN")
	if quint == "" {
		t.Skip("QUINT_BIN not set; skipping Quint typecheck")
	}
	if out, err := exec.Command(quint, "typecheck", filepath.Join(out, "Model.qnt")).CombinedOutput(); err != nil {
		t.Fatalf("quint typecheck: %v\n%s", err, out)
	}
}

// A List call fills a list local with the identities of the existing
// objects of the namespace the call names; len reads its size (ADR-0011).
func TestGenerateList(t *testing.T) {
	out, err := generateTo(t, "testdata/listed/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	if v := spec.State["maps_items"]; v.Type != "list(string)" || v.Initial != "[]" {
		t.Fatalf("list local %+v; state %v", v, slices.Sorted(maps.Keys(spec.State)))
	}
	var listed, sized bool
	for _, a := range spec.Actions {
		if a.Effect == "listMaps" && a.Updates["maps_items"] == `(ConfigMap_source_exists ? ["source"] : []) + (ConfigMap_copy_exists ? ["copy"] : [])` {
			listed = true
		}
		if strings.Contains(string(a.Updates[pcVariable]), "size(maps_items) > 1") {
			sized = true
		}
	}
	if !listed || !sized {
		for _, a := range spec.Actions {
			if a.Effect == "listMaps" {
				t.Logf("%s: %v", a.Name, a.Updates["maps_items"])
			}
		}
		t.Fatalf("listed=%v sized=%v", listed, sized)
	}
	quint := os.Getenv("QUINT_BIN")
	if quint == "" {
		t.Skip("QUINT_BIN not set; skipping Quint typecheck")
	}
	if out, err := exec.Command(quint, "typecheck", filepath.Join(out, "Model.qnt")).CombinedOutput(); err != nil {
		t.Fatalf("quint typecheck: %v\n%s", err, out)
	}
}
