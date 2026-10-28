package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/project"
	"github.com/phoban01/generative-architecture/pkg/report"
)

const inventoryFile = "cmd/arch/testdata/inventory/controller.go"

// constructs lists the constructs of items at their file:line.
func constructs(items []report.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Construct+" at "+location(it.Location))
	}
	return out
}

// TestExtractReadsReconcile checks that arch extract reads Reconcile with
// no abstraction: a construct the frontend cannot read is unsupported
// and located, every field the body reads or writes is unresolved, and a
// method that extracts is listed as included.
func TestExtractReadsReconcile(t *testing.T) {
	out, err := extractTo(t, "testdata/inventory/project.yaml")
	if !errors.Is(err, errUnsupported) {
		t.Fatalf("expected unsupported constructs, got %v", err)
	}
	s := readSupport(t, out)
	if len(s.Unsupported) != 1 || s.Unsupported[0].Kind != "call" || location(s.Unsupported[0].Location) != inventoryFile+":47" {
		t.Fatalf("unsupported %+v, want the call at line 47", s.Unsupported)
	}
	want := []string{
		"cm.data.a at " + inventoryFile + ":31",
		"cm.metadata.namespace at " + inventoryFile + ":35",
		"secret.metadata.namespace at " + inventoryFile + ":35",
		"cm.metadata.name at " + inventoryFile + ":36",
		"secret.metadata.name at " + inventoryFile + ":36",
		"secret.stringData.a at " + inventoryFile + ":37",
	}
	if got := constructs(s.Unresolved); !slices.Equal(got, want) {
		t.Fatalf("unresolved %q, want %q", got, want)
	}
	var reconciles []string
	for _, it := range s.Included {
		if it.Kind == "reconcile" {
			reconciles = append(reconciles, it.Construct+" at "+location(it.Location))
		}
	}
	if len(reconciles) != 1 || !strings.HasSuffix(reconciles[0], "inventory.Mirror at "+inventoryFile+":22") {
		t.Fatalf("included reconcile methods %q, want Mirror only", reconciles)
	}
}

// TestSupportShowsReconcileDiagnostics checks that arch support prints the
// diagnostics of Reconcile.
func TestSupportShowsReconcileDiagnostics(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--project", "testdata/inventory/project.yaml", "support"}, &stdout, &stderr); err != nil {
		t.Fatalf("inventory mode must pass: %v", err)
	}
	if !strings.Contains(stdout.String(), inventoryFile+":47  unsummarized call to fmt.Println") {
		t.Fatalf("table lacks the Reconcile diagnostic:\n%s", stdout.String())
	}
}

// TestExtractReconcileWithAbstraction checks that the fields the
// abstraction selects resolve, that its map keys complete a map literal
// and that a requeue reaches the program.
func TestExtractReconcileWithAbstraction(t *testing.T) {
	p, err := project.Load("testdata/inventory/abstracted.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := p.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	abs, err := abstraction.Load(filepath.Join(p.Dir, p.Spec.Abstraction))
	if err != nil {
		t.Fatal(err)
	}
	c := ir.Controller{ID: "mirror", Package: "./cmd/arch/testdata/inventory", Receiver: "Mirror"}
	l, err := goflow.Load(p.Root, goflow.Selector{Package: c.Package, Receiver: c.Receiver, Method: reconcileMethod})
	if err != nil {
		t.Fatal(err)
	}
	program, unsupported, unresolved := extractReconcile(p.Root, c, l, &abs, cat)
	if program == nil || len(unsupported) != 0 {
		t.Fatalf("unsupported %+v", unsupported)
	}
	for _, it := range unresolved {
		if strings.Contains(it.Construct, "data.a") || strings.Contains(it.Construct, "stringData") {
			t.Errorf("selected field %s reported unresolved", it.Construct)
		}
	}
	if len(unresolved) != 4 {
		t.Errorf("unresolved %q, want the four metadata fields", constructs(unresolved))
	}
	var cleared, requeued bool
	for _, n := range program.Nodes {
		if n.Updates["secret_stringData_a"] != "" && n.Updates["secret_stringData_b"] == `""` {
			cleared = true
		}
		if n.Kind == ir.NodeReturn && n.Requeue == ir.RequeueAfter {
			requeued = true
		}
	}
	if !cleared {
		t.Error("the map literal does not clear the selected key b")
	}
	if !requeued {
		t.Error("no return node requeues after a delay")
	}
}

func TestIRProcessCopiesRequeue(t *testing.T) {
	program := goflow.Program{Nodes: []goflow.Node{
		{ID: 0, Kind: ir.NodeReturn, Requeue: ir.RequeueAfter, RequeueAfter: "time.Minute"},
		{ID: 1, Kind: ir.NodeReturn},
	}}
	p, err := irProcess(ir.Controller{ID: "c", Name: "c"}, program, nil, "c.go")
	if err != nil {
		t.Fatal(err)
	}
	if p.Nodes[0].Requeue != ir.RequeueAfter || p.Nodes[1].Requeue != "" {
		t.Fatalf("requeues %q and %q", p.Nodes[0].Requeue, p.Nodes[1].Requeue)
	}
}
