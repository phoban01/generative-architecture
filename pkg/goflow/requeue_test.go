package goflow

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

// A reconcile result literal that asks for a requeue is recorded on the
// return node when the result is declared with Requeue.
func TestRequeueResults(t *testing.T) {
	type requeue struct{ kind, after string }
	for _, tc := range []struct {
		method     string
		requeues   []requeue
		diagnostic string
	}{
		{method: "RequeueLater", requeues: []requeue{{ir.RequeueImmediate, ""}, {ir.RequeueAfter, "Delay"}}},
		{method: "RequeueBoth", requeues: []requeue{{ir.RequeueAfter, "time.Minute"}}},
		{method: "RequeueNone", requeues: []requeue{{"", ""}}},
		{method: "RequeueVariable", diagnostic: "Requeue must be a constant"},
		{method: "RequeueByPosition", diagnostic: "reconcile result fields must be set by name"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			opts := copyOptions()
			opts.Returns = []Return{{Zero: true, Requeue: true}, {Error: true}}
			p, err := ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", tc.method), opts)
			if tc.diagnostic != "" {
				d, ok := AsDiagnostic(err)
				if !ok || d.Construct != "return" || d.Line == 0 || !strings.Contains(d.Reason, tc.diagnostic) {
					t.Fatalf("got %v, want a located diagnostic containing %q", err, tc.diagnostic)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []requeue
			for _, n := range p.Nodes {
				if n.Kind == "return" {
					got = append(got, requeue{n.Requeue, n.RequeueAfter})
				}
			}
			// Nodes are added last statement first.
			for i, j := 0, len(got)-1; i < j; i, j = i+1, j-1 {
				got[i], got[j] = got[j], got[i]
			}
			if !reflect.DeepEqual(got, tc.requeues) {
				t.Fatalf("requeues %v, want %v", got, tc.requeues)
			}
		})
	}
	// Without Requeue on the declared result, a requeue is a diagnostic.
	_, err := ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "RequeueBoth"), copyOptions())
	if d, ok := AsDiagnostic(err); !ok || d.Reason != "result must be the zero value of its type" {
		t.Fatalf("requeue accepted without Return.Requeue: %v", err)
	}
}
