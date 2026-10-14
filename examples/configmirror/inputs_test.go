package configmirror

import (
	"path/filepath"
	"reflect"
	"testing"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/project"
)

// The Project and Abstraction documents are the authored inputs Checkpoint A
// generates everything else from. Until then they must load, validate and
// agree with the handwritten controller, adapter and model.

var configMap = ir.GVK{Version: "v1", Kind: "ConfigMap"}

func TestProjectDocument(t *testing.T) {
	p, err := project.Load("project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	here, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Spec.Packages, []string{"./examples/configmirror"}) || filepath.Join(p.Root, "examples", "configmirror") != here {
		t.Fatalf("project does not select this package: %v under %s", p.Spec.Packages, p.Root)
	}
	if !reflect.DeepEqual(p.Spec.Constraints, []string{"constraints.yaml"}) || p.Hashes["constraints.yaml"] == "" || p.Hashes["discovery.json"] == "" {
		t.Fatalf("constraints %v, hashes %v", p.Spec.Constraints, p.Hashes)
	}
	c, err := p.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := c.Lookup(configMap)
	if !ok || e.GVR.Resource != "configmaps" || e.Scope != ir.Namespaced {
		t.Fatalf("catalog entry for ConfigMap: %+v (%v)", e, ok)
	}
	if len(c.Entries()) != 1 {
		t.Fatalf("catalog %v", c.Entries())
	}
}

func TestAbstractionDocument(t *testing.T) {
	a, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Spec.Resources) != 1 || a.Spec.Resources[0].GVK != configMap {
		t.Fatalf("resources %+v", a.Spec.Resources)
	}
	r := a.Spec.Resources[0]
	// The objects are exactly the fixtures the adapter creates and the
	// reconciler reads and writes.
	want := []abstraction.Object{{ID: "source", Namespace: sourceKey.Namespace, Name: sourceKey.Name}, {ID: "copy", Namespace: targetKey.Namespace, Name: targetKey.Name}}
	if !reflect.DeepEqual(r.Objects, want) {
		t.Fatalf("objects %+v, want %+v", r.Objects, want)
	}
	if len(r.Fields) != 1 || abstraction.PathKey(r.Fields[0].Path) != "data.value" || r.Fields[0].Type != abstraction.TypeString {
		t.Fatalf("fields %+v", r.Fields)
	}
	if !reflect.DeepEqual(r.ValueDomains["data.value"], []string{`"a"`, `"b"`, `""`}) {
		t.Fatalf("domain %v", r.ValueDomains)
	}
	if !reflect.DeepEqual(a.Spec.Locals, []abstraction.Local{{Name: "snapshot", Type: abstraction.TypeString}}) {
		t.Fatalf("locals %+v", a.Spec.Locals)
	}
	if a.Spec.Bounds.MaxObjectsPerResource != 2 || a.Spec.Bounds.MaxInvocations != 3 || !reflect.DeepEqual(a.Spec.Excludes, []string{"delete", "list"}) {
		t.Fatalf("bounds %+v excludes %v", a.Spec.Bounds, a.Spec.Excludes)
	}
	wantVars := []string{"ConfigMap_copy_data_value", "ConfigMap_copy_exists", "ConfigMap_source_data_value", "ConfigMap_source_exists", "snapshot"}
	if got := a.SortedVariables(); !reflect.DeepEqual(got, wantVars) {
		t.Fatalf("variables %v, want %v", got, wantVars)
	}
}

func TestDocumentsAgreeWithHandwrittenModel(t *testing.T) {
	p, err := project.Load("project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	a, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Bounds["maxObjectsPerResource"] != a.Spec.Bounds.MaxObjectsPerResource || p.Spec.Bounds["maxInvocations"] != a.Spec.Bounds.MaxInvocations {
		t.Fatalf("project bounds %v differ from abstraction bounds %+v", p.Spec.Bounds, a.Spec.Bounds)
	}
	m, err := Load("system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Every effect of the handwritten model touches the abstracted resource,
	// with the operations the abstraction keeps.
	resource := effect.Resource{Group: configMap.Group, Version: configMap.Version, Kind: configMap.Kind}
	descriptors := m.Effects().Descriptors()
	if len(descriptors) != 3 {
		t.Fatalf("effects %+v", descriptors)
	}
	excluded := map[string]bool{}
	for _, op := range a.Spec.Excludes {
		excluded[op] = true
	}
	for _, d := range descriptors {
		if d.Resource != resource {
			t.Fatalf("effect %s touches %+v, not the abstracted ConfigMap", d.ID, d.Resource)
		}
		if excluded[d.Operation] {
			t.Fatalf("effect %s uses excluded operation %s", d.ID, d.Operation)
		}
	}
	// The local the abstraction keeps is a state variable of the model.
	var spec model.Spec
	if _, err = krm.Read("system.yaml", "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	for _, l := range a.Spec.Locals {
		if v, ok := spec.State[l.Name]; !ok || v.Type != l.Type {
			t.Fatalf("local %s (%s) is not model state: %+v", l.Name, l.Type, spec.State)
		}
	}
}

func loadConstraints(t *testing.T) []model.Property {
	t.Helper()
	var requirements struct {
		Properties []model.Property `yaml:"properties"`
	}
	if _, err := krm.Read("constraints.yaml", "ModelConstraints", &requirements); err != nil {
		t.Fatal(err)
	}
	return requirements.Properties
}

func TestConstraintsEvaluateAgainstSavedTraces(t *testing.T) {
	properties := loadConstraints(t)
	var spec model.Spec
	if _, err := krm.Read("system.yaml", "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	// The independent document carries the model's properties verbatim until
	// Checkpoint A removes them from system.yaml.
	if !reflect.DeepEqual(properties, spec.Properties) {
		t.Fatalf("constraints.yaml %+v differs from system.yaml properties %+v", properties, spec.Properties)
	}
	spec.Properties = properties
	constrained, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob("traces/*.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 40 {
		t.Fatalf("expected 40 recorded traces, got %d", len(paths))
	}
	commits := 0
	for _, path := range paths {
		trace, err := conformance.ReadITF(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, s := range trace.States {
			if err = constrained.Check(s.Values, s.Event); err != nil {
				t.Fatalf("%s step %d (%s): %v", filepath.Base(path), i, s.Event, err)
			}
			if s.Event == "commit" {
				commits++
			}
		}
	}
	if commits == 0 {
		t.Fatal("no trace exercises writesObservedValue")
	}
	// The check is not vacuous: a constraint the traces violate is reported.
	spec.Properties = append(properties, model.Property{Name: "neverCommits", Expression: `event != "commit"`})
	violating, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	violated := false
	for _, path := range paths {
		trace, err := conformance.ReadITF(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range trace.States {
			if err = violating.Check(s.Values, s.Event); err != nil {
				violated = true
			}
		}
	}
	if !violated {
		t.Fatal("neverCommits was not violated by any saved trace")
	}
}
