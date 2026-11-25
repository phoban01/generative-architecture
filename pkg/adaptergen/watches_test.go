package adaptergen_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/ir"
)

func databaseWatches() adaptergen.WatchDelivery {
	database := ir.GVK{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "Database"}
	instance := ir.GVK{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "DatabaseInstance"}
	return adaptergen.WatchDelivery{
		Key: ir.ObjectRef{GVK: database, Namespace: "demo", Name: "db-1"},
		Kinds: []adaptergen.WatchKind{
			{Resource: database, Namespace: "demo", Name: "db-1"},
			{Resource: instance, Namespace: "demo", Name: "instance-for-db-1", Fields: []adaptergen.WatchField{{GoPath: []string{"Spec", "DatabaseName"}, JSONPath: []string{"spec", "databaseName"}, Initial: `"db-1"`}}},
		},
	}
}

// The watch delivery typechecks without a package of this module, routes
// each rule it is given through the native predicates and handlers, builds
// typed or unstructured objects as the rule watches them and observes the
// watch model's variables.
func TestGenerateWatchDelivery(t *testing.T) {
	src, err := adaptergen.GenerateWatchDelivery("p", databaseWatches())
	if err != nil {
		t.Fatal(err)
	}
	p := typecheck(t, src)
	assertNoModelState(t, p)
	for _, imp := range p.Syntax[0].Imports {
		if strings.Contains(imp.Path.Value, "generative-architecture") {
			t.Errorf("generated delivery imports %s", imp.Path.Value)
		}
	}
	params, _ := methodSignature(t, p, "Watches", "Deliver")
	if want := []string{"context.Context", "string", "[]github.com/phoban01/generative-architecture/pkg/adaptergen/internal/typecheck.WatchRule", "github.com/phoban01/generative-architecture/pkg/adaptergen/internal/typecheck.WatchQueue", "map[string]sigs.k8s.io/controller-runtime/pkg/handler.EventHandler", "map[string]func() sigs.k8s.io/controller-runtime/pkg/client.Object"}; !slices.Equal(params, want) {
		t.Errorf("Deliver(%v), want %v", params, want)
	}
	text := string(src)
	for _, want := range []string{
		`case "deliverDatabase":` + "\n\t\treturn \"Database\", true",
		`into["queued_demo_db_1"] = queue.Queued()`,
		`into["wrongKey"] = queue.Err() != nil`,
		`into["databaseInstance_Spec_DatabaseName"] = o.fields["databaseInstance_Spec_DatabaseName"]`,
		`into["databaseInstance_Spec_DatabaseNameSeen"] = o.fieldsSeen["databaseInstance_Spec_DatabaseName"]`,
		`var h handler.EventHandler = &handler.EnqueueRequestForObject{}`,
		`unstructured.SetNestedField(u.Object, fields["databaseInstance_Spec_DatabaseName"], "spec", "databaseName")`,
		`u.SetName("instance-for-db-1")`,
		`case "generationChanged":`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated delivery lacks %q:\n%s", want, text)
		}
	}
}

func TestGenerateWatchDeliveryRejects(t *testing.T) {
	for name, edit := range map[string]func(*adaptergen.WatchDelivery){
		"no kinds":    func(d *adaptergen.WatchDelivery) { d.Kinds = nil },
		"no key":      func(d *adaptergen.WatchDelivery) { d.Key = ir.ObjectRef{} },
		"bad initial": func(d *adaptergen.WatchDelivery) { d.Kinds[1].Fields[0].Initial = "db-1" },
		"twice":       func(d *adaptergen.WatchDelivery) { d.Kinds = append(d.Kinds, d.Kinds[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			d := databaseWatches()
			edit(&d)
			if _, err := adaptergen.GenerateWatchDelivery("p", d); err == nil {
				t.Fatal("generated a delivery from an invalid description")
			}
		})
	}
}
