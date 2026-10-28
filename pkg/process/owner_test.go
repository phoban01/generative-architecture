package process_test

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/process"
)

// A reconciler that calls controllerutil.SetControllerReference extracts
// without a diagnostic, and the model holds the owner reference after it.
func TestOwnerReferenceInModel(t *testing.T) {
	l, err := goflow.Load("../..", goflow.Selector{Package: "./pkg/goflow/testdata/kube", Receiver: "Reconciler", Method: "Own"})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{
		"secret.metadata.name":                       "secret_name",
		"cm.metadata.name":                           "cm_name",
		"secret.metadata.ownerReferences.name":       "secret_owner",
		"secret.metadata.ownerReferences.controller": "secret_controller",
	}
	types := map[string]string{"secret_controller": "bool"}
	opts := goflow.Options{Scope: map[string]expr.Symbol{}, Returns: []goflow.Return{{Zero: true}, {Error: true}}}
	for _, symbol := range fields {
		typ := types[symbol]
		if typ == "" {
			typ = "string"
		}
		opts.Scope[symbol] = expr.Symbol{Type: typ, Quint: symbol}
	}
	opts.Field = func(root string, path []string) (expr.Expression, bool) {
		s, ok := fields[root+"."+strings.Join(path, ".")]
		return expr.Expression(s), ok
	}
	p, err := goflow.ExtractTyped(l, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		owner expr.Expression
		want  string
	}{
		{`"app"`, "app"},
		{`"db"`, "db"},
	} {
		state := map[string]model.Variable{
			"cm_name":           {Type: "string", Initial: tc.owner},
			"secret_name":       {Type: "string", Initial: `""`},
			"secret_owner":      {Type: "string", Initial: `""`},
			"secret_controller": {Type: "bool", Initial: "false"},
			"err":               {Type: "bool", Initial: "true"},
		}
		spec, err := process.Expand(model.Spec{Name: "Owner", State: state}, *p, process.Config{PC: "pc", Finished: "done", Results: []string{"", ""}})
		if err != nil {
			t.Fatal(err)
		}
		final := finish(t, spec)
		if final["secret_owner"] != tc.want || final["secret_controller"] != true || final["secret_name"] != tc.want || final["err"] != false {
			t.Fatalf("owner %s: final state %v", tc.owner, final)
		}
	}
}
