package generated

import (
	"context"
	"testing"

	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/adaptergen/internal/fixture"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestEnvironmentUpToDate(t *testing.T) {
	src, err := adaptergen.GenerateEnvironment(fixture.Package, fixture.Actions())
	if err != nil {
		t.Fatal(err)
	}
	assertGenerated(t, "environment.go", src)
}

func TestApplyEnvironment(t *testing.T) {
	ctx := context.Background()
	api, mapper := newAPI(t)
	apply := func(event string) error {
		t.Helper()
		handled, err := ApplyEnvironment(ctx, api, mapper, event)
		if !handled {
			t.Fatalf("%s was not handled", event)
		}
		return err
	}
	size := func() string {
		t.Helper()
		mapping, err := mapper.RESTMapping(fixture.Database.Schema().GroupKind(), fixture.Database.Version)
		if err != nil {
			t.Fatal(err)
		}
		obj, err := api.Resource(mapping.Resource).Namespace("demo").Get(ctx, "db-1", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		v, _, err := unstructured.NestedString(obj.Object, "spec", "size")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	project := func() map[string]any {
		t.Helper()
		state, err := Project(ctx, api, mapper)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}

	if handled, err := ApplyEnvironment(ctx, api, mapper, "node1"); handled || err != nil {
		t.Fatalf("process event handled=%v err=%v; want neither", handled, err)
	}
	if err := apply("resizeDatabase"); !apierrors.IsNotFound(err) {
		t.Fatalf("update of a missing object: %v, want not found", err)
	}
	if err := apply("discoverDatabase"); err != nil {
		t.Fatal(err)
	}
	if s := project(); s["Database_db1_exists"] != true || size() != "small" {
		t.Fatalf("after create: %v size %q", s, size())
	}
	if err := apply("discoverDatabase"); !apierrors.IsAlreadyExists(err) {
		t.Fatalf("second create: %v, want already exists", err)
	}
	if err := apply("resizeDatabase"); err != nil {
		t.Fatal(err)
	}
	if size() != "large" {
		t.Fatalf("after update: size %q", size())
	}
	if err := apply("createInstance"); err != nil {
		t.Fatal(err)
	}
	if err := apply("provisionInstance"); err != nil {
		t.Fatal(err)
	}
	s := project()
	if s["DatabaseInstance_inst1_spec_databaseName"] != "db-1" || s["DatabaseInstance_inst1_status_endpoint"] != "db-1.internal" {
		t.Fatalf("after status update: %v", s)
	}
	if err := apply("removeDatabase"); err != nil {
		t.Fatal(err)
	}
	if s := project(); s["Database_db1_exists"] != false || s["DatabaseInstance_inst1_exists"] != true {
		t.Fatalf("after delete: %v", s)
	}
}
