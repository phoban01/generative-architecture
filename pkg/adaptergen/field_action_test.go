package adaptergen_test

import (
	"reflect"
	"strings"
	"testing"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/adaptergen/internal/fixture"
	"generative-architecture/pkg/ir"
)

// FieldAction turns a model environment action into an API operation: a
// predicate over an annotation key, whose path element holds dots, is
// written as the value it compares to, and a status field is updated
// through the status subresource.
func TestFieldAction(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	contract := []string{"metadata", "annotations", "example.org/contract"}
	r := &a.Spec.Resources[0]
	r.Fields = append(r.Fields, abstraction.Field{Path: contract, Name: "contract", Type: abstraction.TypeBool, Equals: `"v1"`})
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	db := ir.ObjectRef{GVK: fixture.Database, Namespace: "demo", Name: "db-1"}
	inst := ir.ObjectRef{GVK: fixture.Instance, Namespace: "demo", Name: "instance-for-db-1"}
	for _, tc := range []struct {
		op, object, variable, literal string
		want                          adaptergen.EnvAction
	}{
		{adaptergen.ActionCreate, "db1", "Database_db1_contract", "true", adaptergen.EnvAction{Operation: adaptergen.ActionCreate, Resource: fixture.Database, Key: db, Values: []adaptergen.FieldValue{{Path: contract, Value: "v1"}}}},
		{adaptergen.ActionCreate, "db1", "Database_db1_contract", "false", adaptergen.EnvAction{Operation: adaptergen.ActionCreate, Resource: fixture.Database, Key: db}},
		{adaptergen.ActionUpdate, "inst1", "DatabaseInstance_inst1_status_endpoint", `"db-1.internal"`, adaptergen.EnvAction{Operation: adaptergen.ActionStatus, Resource: fixture.Instance, Key: inst, Values: []adaptergen.FieldValue{{Path: []string{"status", "endpoint"}, Value: "db-1.internal"}}}},
		{adaptergen.ActionUpdate, "inst1", "DatabaseInstance_inst1_spec_databaseName", `"db-1"`, adaptergen.EnvAction{Operation: adaptergen.ActionUpdate, Resource: fixture.Instance, Key: inst, Values: []adaptergen.FieldValue{{Path: []string{"spec", "databaseName"}, Value: "db-1"}}}},
	} {
		got, err := adaptergen.FieldAction(a.Spec, "e", tc.op, tc.object, tc.variable, tc.literal)
		if err != nil {
			t.Fatal(err)
		}
		tc.want.Name = "e"
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %s = %s: got %+v, want %+v", tc.op, tc.variable, tc.literal, got, tc.want)
		}
	}
	created, _ := adaptergen.FieldAction(a.Spec, "createDb1", adaptergen.ActionCreate, "db1", "Database_db1_contract", "true")
	src, err := adaptergen.GenerateEnvironment("p", []adaptergen.EnvAction{created})
	if err != nil {
		t.Fatal(err)
	}
	if want := `unstructured.SetNestedField(obj.Object, "v1", "metadata", "annotations", "example.org/contract")`; !strings.Contains(string(src), want) {
		t.Errorf("generated environment lacks %q:\n%s", want, src)
	}
	for _, tc := range []struct{ op, variable, literal, want string }{
		{adaptergen.ActionUpdate, "Database_db1_contract", "false", "cannot update predicate"},
		{adaptergen.ActionCreate, "Database_db1_missing", `"x"`, "declares no field"},
		{adaptergen.ActionDelete, "Database_db1_contract", "true", "expected create or update"},
		{adaptergen.ActionCreate, "Database_db1_status_endpoint", "7", "not a string literal"},
	} {
		if _, err := adaptergen.FieldAction(a.Spec, "e", tc.op, "db1", tc.variable, tc.literal); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %s = %s: expected an error containing %q, got %v", tc.op, tc.variable, tc.literal, tc.want, err)
		}
	}
}
