package configmirror

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/project"
)

// Set CONFIGMIRROR_REGENERATE=1 to rewrite the committed generated files
// from the current sources instead of comparing against them.
const regenerate = "CONFIGMIRROR_REGENERATE"

// extractReconcile loads Reconcile with type information and extracts it
// with ExtractOptions.
func extractReconcile(t *testing.T) (goflow.Options, *goflow.Program) {
	t.Helper()
	p, err := project.Load("project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	abs, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		t.Fatal(err)
	}
	l, err := goflow.Load(p.Root, goflow.Selector{Package: p.Spec.Packages[0], Receiver: "Reconciler", Method: "Reconcile"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := ExtractOptions(l, abs)
	if err != nil {
		t.Fatal(err)
	}
	program, err := goflow.ExtractTyped(l, opts)
	if err != nil {
		t.Fatal(err)
	}
	return opts, program
}

func TestExtractReconcileWithTypedFrontend(t *testing.T) {
	opts, program := extractReconcile(t)
	// Every call is resolved by callee symbol; nothing matches source text.
	if opts.Statement != nil || opts.Bindings != nil || opts.Targets != nil || opts.Call == nil {
		t.Fatalf("extraction must use Options.Call only: statement %v, bindings %v, targets %v", opts.Statement != nil, opts.Bindings, opts.Targets)
	}
	if program.SourceSHA256 == "" || program.MethodSHA256 == "" {
		t.Fatalf("provenance hashes missing: %+v", program)
	}

	type effectNode struct{ Effect, Kind, Consistency string }
	var effects []effectNode
	var branches, returns []goflow.Node
	byID := map[int]goflow.Node{}
	for _, n := range program.Nodes {
		byID[n.ID] = n
		switch {
		case n.Effect != "":
			effects = append(effects, effectNode{n.Effect, n.Kind, n.Consistency})
			if n.Error != goflow.ReturnedError {
				t.Fatalf("effect %s does not bind its error: %+v", n.Effect, n)
			}
		case n.Kind == "branch":
			branches = append(branches, n)
		case n.Kind == "return":
			returns = append(returns, n)
		}
	}
	sort.Slice(effects, func(i, j int) bool { return effects[i].Effect < effects[j].Effect })
	want := []effectNode{
		{"readSource", ir.OpGet, ir.ConsistencyCached},
		{"readTarget", ir.OpGet, ir.ConsistencyDirect},
		{"writeTarget", ir.OpUpdate, ir.ConsistencyDirect},
	}
	if !reflect.DeepEqual(effects, want) {
		t.Fatalf("effect nodes %+v, want %+v", effects, want)
	}
	// Both err != nil tests branch to a return of the error.
	if len(branches) != 2 {
		t.Fatalf("expected two branches, got %+v", branches)
	}
	for _, b := range branches {
		if b.Expression != "err" || byID[b.Next].Kind != "return" || byID[b.Next].ReturnError != "err" {
			t.Fatalf("branch is not err != nil returning err: %+v -> %+v", b, byID[b.Next])
		}
	}
	// The final return returns the error of the update it performs.
	if len(returns) != 3 {
		t.Fatalf("expected three returns, got %+v", returns)
	}
	var final *goflow.Node
	for _, n := range program.Nodes {
		if n.Effect == "writeTarget" {
			next := byID[n.Next]
			final = &next
		}
	}
	if final == nil || final.Kind != "return" || final.ReturnError != goflow.ReturnedError || final.Source != "return reconcile.Result{}, r.Client.Update(ctx, target)" {
		t.Fatalf("final return does not return the update's error: %+v", final)
	}
	// The entry is the cached read: declarations produce no nodes.
	if program.Nodes[program.Entry].Effect != "readSource" {
		t.Fatalf("entry %+v", program.Nodes[program.Entry])
	}

	path := filepath.Join("generated", "process.yaml")
	if os.Getenv(regenerate) != "" {
		if err := krm.WriteResult(path, "ExtractedProcess", "config-mirror", program); err != nil {
			t.Fatal(err)
		}
	}
	var committed goflow.Program
	if err := krm.ReadResult(path, "ExtractedProcess", &committed); err != nil {
		t.Fatalf("%v; run with %s=1 to write it", err, regenerate)
	}
	if !reflect.DeepEqual(&committed, program) {
		t.Fatalf("%s is stale; run with %s=1 to regenerate:\ncommitted %+v\nextracted %+v", path, regenerate, committed, *program)
	}
}
