package database

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/process"
)

// The model is generated: its state holds every abstraction variable with
// the abstraction's type and initial value, its effects come from the
// dynamic accesses plus the one derived read, and the committed
// generated/model.yaml and generated/Model.qnt are current.
func TestGenerateModel(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	abs, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range abs.StateVariables() {
		if got, ok := e.Spec.State[name]; !ok || got != v {
			t.Errorf("state %s = %+v, want %+v", name, got, v)
		}
	}
	database := effect.Resource{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "Database"}
	instance := effect.Resource{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "DatabaseInstance"}
	want := []effect.Descriptor{
		{ID: "checkContract", Resource: database, Operation: "derive", Consistency: "schema"},
		{ID: "createInstance", Resource: instance, Operation: "create", Consistency: "direct"},
		{ID: "publish", Resource: database, Operation: "status.update", Consistency: "direct"},
		{ID: "readDatabase", Resource: database, Operation: "get", Consistency: "direct"},
		{ID: "readInstance", Resource: instance, Operation: "get", Consistency: "direct"},
	}
	if !reflect.DeepEqual(e.Spec.Effects, want) {
		t.Errorf("effects %+v, want %+v", e.Spec.Effects, want)
	}
	var saved model.Spec
	if _, err := krm.Read(filepath.Join("generated", "model.yaml"), "TransitionModel", &saved); err != nil {
		t.Fatalf("%v; run go run ./cmd/walkthrough", err)
	}
	// An empty property list reads back as nil.
	spec := e.Spec
	if len(spec.Properties) == 0 {
		spec.Properties, saved.Properties = nil, nil
	}
	if !reflect.DeepEqual(saved, spec) {
		t.Fatal("generated/model.yaml is stale; run go run ./cmd/walkthrough")
	}
	committed, err := os.ReadFile(filepath.Join("generated", "Model.qnt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != e.Constrained.Quint() {
		t.Fatal("generated/Model.qnt is stale; run go run ./cmd/walkthrough")
	}
	quint := os.Getenv("QUINT_BIN")
	if quint == "" {
		t.Skip("QUINT_BIN not set; skipping Quint typecheck of the generated model")
	}
	if out, err := exec.Command(quint, "typecheck", filepath.Join("generated", "Model.qnt")).CombinedOutput(); err != nil {
		t.Fatalf("quint typecheck: %v\n%s", err, out)
	}
}

// The environment owns exactly the fields the Store's summaries do not
// write: it creates the Database with each contract value before the first
// invocation, and fills the instance's endpoint once.
func TestGeneratedEnvironment(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	want := []EnvironmentAction{
		{Name: "createDb1_false", Operation: EnvCreate, Kind: "Database", Object: "db1", Variable: "Database_db1_contractSatisfied", Value: "false"},
		{Name: "createDb1_true", Operation: EnvCreate, Kind: "Database", Object: "db1", Variable: "Database_db1_contractSatisfied", Value: "true"},
		{Name: "changeInst1_1", Operation: EnvUpdate, Kind: "DatabaseInstance", Object: "inst1", Variable: "DatabaseInstance_inst1_status_endpoint", Value: `"db-1.internal"`},
	}
	if !reflect.DeepEqual(e.Environment, want) {
		t.Fatalf("environment %+v, want %+v", e.Environment, want)
	}
	state, err := e.Model.Initial()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Model.Next(state, "changeInst1_1"); err == nil {
		t.Fatal("endpoint provisioned for an absent instance")
	}
	created, err := e.Model.Next(state, "createDb1_true")
	if err != nil {
		t.Fatal(err)
	}
	if created["Database_db1_exists"] != true || created["Database_db1_contractSatisfied"] != true {
		t.Fatalf("create did not set the object: %v", created)
	}
	if _, err := e.Model.Next(created, "createDb1_false"); err == nil {
		t.Fatal("Database created twice")
	}
}

// Replay observes every abstraction variable, and the adapter projects
// each of them under the model's name.
func TestObservedCoversAbstraction(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	abs, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := New(ctx, e, false)
	defer a.Close()
	projected, err := a.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range abs.SortedVariables() {
		if !strings.Contains(" "+strings.Join(e.Observed, " ")+" ", " "+name+" ") {
			t.Errorf("replay does not observe %s", name)
		}
		if _, ok := projected[name]; !ok {
			t.Errorf("adapter does not project %s", name)
		}
	}
	for _, name := range e.Observed {
		if _, ok := e.Spec.State[name]; !ok {
			t.Errorf("observed %s is not model state", name)
		}
	}
}

// Generation fails closed on inputs it cannot express.
func TestGenerateRejectsUnsupportedInputs(t *testing.T) {
	abs, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config process.Config
	if _, err := krm.Read("process.yaml", "ProcessSemantics", &config); err != nil {
		t.Fatal(err)
	}
	// Without the summary that writes the Database's status, the
	// environment would own two of its fields.
	reduced := config
	reduced.Operations = map[string][]process.Variant{}
	for k, v := range config.Operations {
		if k != "publish" {
			reduced.Operations[k] = v
		}
	}
	written, err := controllerWrites(abs, reduced)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := environmentActions(abs, written); err == nil || !strings.Contains(err.Error(), "owns 2 fields") {
		t.Fatalf("two environment fields accepted: %v", err)
	}
	// A summary may assign only abstraction variables.
	bad := process.Config{Operations: map[string][]process.Variant{"x": {{Updates: map[string]expr.Expression{"unknown": "1"}}}}}
	if _, err := controllerWrites(abs, bad); err == nil {
		t.Fatal("summary of an undeclared variable accepted")
	}
	// A Store call of a method without an effect is a diagnostic.
	source, err := os.ReadFile("controller.go")
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(source), "UpdateDatabaseStatus(endpoint string)\n", "UpdateDatabaseStatus(endpoint string)\n\tAudit()\n", 1)
	mutated = strings.Replace(mutated, "\treturn \"ready\"", "\tstore.Audit()\n\treturn \"ready\"", 1)
	if _, err := LoadSource(".", []byte(mutated)); err == nil || !strings.Contains(err.Error(), "Audit") {
		t.Fatalf("undeclared Store method accepted: %v", err)
	}
}
