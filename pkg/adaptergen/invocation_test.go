package adaptergen_test

import (
	"go/types"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"golang.org/x/tools/go/packages"
)

func databaseInvocation() adaptergen.InvocationBinding {
	return adaptergen.InvocationBinding{
		Reconcile: "reconcile", Idle: "idle", Counter: "invocations", Outcome: "outcome",
		Locals:   map[string][]string{"readDatabase": {"found"}, "checkContract": {"compatible"}, "readInstance": {"instanceFound", "endpoint"}, "readName": {"databaseName"}},
		Types:    map[string]string{"found": "bool", "compatible": "bool", "instanceFound": "bool", "endpoint": "string", "databaseName": "string"},
		Counters: map[string]string{"createInstance": "instanceCreates"},
	}
}

// The invocation bookkeeping typechecks, exposes the documented methods and
// records the locals, the counters and the outcome under the model's names.
func TestGenerateInvocation(t *testing.T) {
	src, err := adaptergen.GenerateInvocation("p", databaseInvocation())
	if err != nil {
		t.Fatal(err)
	}
	p := typecheck(t, src)
	assertNoModelState(t, p)
	for name, want := range map[string][]string{"NewInvocation": {}, "Apply": {"string", "func() error"}, "Observe": {"string", "github.com/phoban01/generative-architecture/pkg/effect.Result"}, "Finish": {"string"}, "Project": {}} {
		if name == "NewInvocation" {
			if _, results := signature(t, p, name); !reflect.DeepEqual(results, []string{"*github.com/phoban01/generative-architecture/pkg/adaptergen/internal/typecheck.Invocation"}) {
				t.Errorf("%s returns %v", name, results)
			}
			continue
		}
		if params, _ := methodSignature(t, p, "Invocation", name); !slices.Equal(params, want) {
			t.Errorf("%s(%v), want %v", name, params, want)
		}
	}
	text := string(src)
	for _, want := range []string{
		`case "idle":`,
		`case "reconcile":`,
		`i.locals["instanceFound"] = false`,
		`i.locals["endpoint"] = ""`,
		`if effect == "readInstance" {` + "\n\t\tif v, ok := r.Snapshot[\"result1\"]; ok {\n\t\t\ti.locals[\"endpoint\"] = v",
		`case "createInstance":` + "\n\t\ti.instanceCreates++",
		`out := map[string]any{"invocations": i.count, "outcome": i.outcome}`,
		`out["instanceCreates"] = i.instanceCreates`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated invocation lacks %q:\n%s", want, text)
		}
	}
}

func TestGenerateInvocationRejects(t *testing.T) {
	for name, edit := range map[string]func(*adaptergen.InvocationBinding){
		"same events":     func(b *adaptergen.InvocationBinding) { b.Idle = b.Reconcile },
		"no outcome":      func(b *adaptergen.InvocationBinding) { b.Outcome = "" },
		"untyped local":   func(b *adaptergen.InvocationBinding) { delete(b.Types, "endpoint") },
		"counter collide": func(b *adaptergen.InvocationBinding) { b.Counters["createInstance"] = "found" },
	} {
		t.Run(name, func(t *testing.T) {
			b := databaseInvocation()
			edit(&b)
			if _, err := adaptergen.GenerateInvocation("p", b); err == nil {
				t.Fatal("generated bookkeeping from an invalid binding")
			}
		})
	}
}

// methodSignature returns the parameter and result type strings of a
// method of a named type of the typechecked package.
func methodSignature(t *testing.T, p *packages.Package, typeName, method string) (params, results []string) {
	t.Helper()
	obj := p.Types.Scope().Lookup(typeName)
	named, ok := obj.(*types.TypeName)
	if !ok {
		t.Fatalf("generated source does not define type %s", typeName)
	}
	set := types.NewMethodSet(types.NewPointer(named.Type()))
	sel := set.Lookup(p.Types, method)
	if sel == nil {
		t.Fatalf("type %s has no method %s", typeName, method)
	}
	sig := sel.Type().(*types.Signature)
	for i := 0; i < sig.Params().Len(); i++ {
		params = append(params, sig.Params().At(i).Type().String())
	}
	for i := 0; i < sig.Results().Len(); i++ {
		results = append(results, sig.Results().At(i).Type().String())
	}
	return params, results
}
