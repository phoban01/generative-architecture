package adaptergen_test

import (
	"reflect"
	"strings"
	"testing"

	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/adaptergen/internal/fixture"
	cr "generative-architecture/pkg/controllerruntime"
)

func TestGenerateRuntime(t *testing.T) {
	m, err := fixture.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	src, err := adaptergen.GenerateRuntime(fixture.Package, m, fixture.RuntimeBinding())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "runtime", src)
	p := typecheck(t, src)
	assertNoModelState(t, p)
	// The routing reaches no package of this module: the worker is an
	// interface the file declares.
	for _, imp := range p.Syntax[0].Imports {
		t.Errorf("generated runtime imports %s", imp.Path.Value)
	}
	params, results := signature(t, p, "ApplyRuntime")
	if !reflect.DeepEqual(params, []string{"generative-architecture/pkg/adaptergen/internal/typecheck.RuntimeWorker", "string"}) || !reflect.DeepEqual(results, []string{"bool", "error"}) {
		t.Errorf("ApplyRuntime(%v) %v", params, results)
	}
	params, results = signature(t, p, "ProjectRuntime")
	if !reflect.DeepEqual(params, []string{"generative-architecture/pkg/adaptergen/internal/typecheck.RuntimeWorker"}) || !reflect.DeepEqual(results, []string{"map[string]any"}) {
		t.Errorf("ProjectRuntime(%v) %v", params, results)
	}
	text := string(src)
	for _, want := range []string{
		`case "begin":` + "\n\t\treturn true, worker.StartOne()",
		`case "finishError":` + "\n\t\treturn true, worker.Wait()",
		`case "enqueue":` + "\n\t\tworker.Enqueue()\n\t\treturn true, nil",
		`now, due := worker.Clock()`,
		`"ready":      worker.Ready(),`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated runtime lacks %q:\n%s", want, text)
		}
	}
}

func TestGenerateRuntimeRejects(t *testing.T) {
	m, err := fixture.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	two, err := cr.BuildGroupModel(cr.GroupProfile{Name: "two", MaxTicks: 3, Controllers: []cr.ModelController{{
		Name: "c", Workers: 1, RetryTicks: 1, AfterTicks: 1,
		Keys: []cr.ModelKey{{ID: "a", Namespace: "n", Name: "a"}, {ID: "b", Namespace: "n", Name: "b"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	priority, err := cr.BuildGroupModel(cr.GroupProfile{Name: "priority", MaxTicks: 3, Queue: cr.QueuePriority, Controllers: []cr.ModelController{{
		Name: "c", Workers: 1, RetryTicks: 1, AfterTicks: 1, Keys: []cr.ModelKey{{ID: "a", Namespace: "n", Name: "a"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	events := func(e map[string]string) adaptergen.RuntimeBinding { return adaptergen.RuntimeBinding{Events: e} }
	for name, tc := range map[string]struct {
		model   cr.GroupModel
		binding adaptergen.RuntimeBinding
		want    string
	}{
		"two keys":         {two, events(map[string]string{"advance": "advance"}), "one key"},
		"priority queue":   {priority, events(map[string]string{"advance": "advance"}), "FIFO"},
		"no events":        {m, adaptergen.RuntimeBinding{}, "at least one"},
		"unknown action":   {m, events(map[string]string{"db_db1_list": "list"}), "not an action"},
		"duplicate event":  {m, events(map[string]string{"db_db1_success": "finish", "db_db1_error": "finish"}), "distinct event"},
		"unknown variable": {m, adaptergen.RuntimeBinding{Events: map[string]string{"advance": "advance"}, Variables: map[string]string{"pc": "pc"}}, "not a state variable"},
		"order":            {m, adaptergen.RuntimeBinding{Events: map[string]string{"advance": "advance"}, Variables: map[string]string{"db_db1_order": "order"}}, "does not observe"},
		"duplicate name":   {m, adaptergen.RuntimeBinding{Events: map[string]string{"advance": "advance"}, Variables: map[string]string{"now": "x", "db_db1_due": "x"}}, "distinct model name"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := adaptergen.GenerateRuntime("p", tc.model, tc.binding)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error containing %q, got %v", tc.want, err)
			}
		})
	}
}
