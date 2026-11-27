package goflow

import (
	"go/ast"
	"strconv"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// A loop element is an object identity (ADR-0011). A client call whose
// object is an element addresses one object per identity, so the frontend
// fans the call out: one branch per identity guards the node for that
// object. The generator asks for the fan-out by returning a *FanOut from
// Options.Call, and builds each object's node in Options.ElementCall.

// FanOut is the error Options.Call returns for a call whose object is the
// loop element Root, which ranges over the identities IDs.
type FanOut struct {
	Root string
	IDs  []string
}

func (f *FanOut) Error() string {
	return "call on loop element " + f.Root + " fans out over its objects"
}

// fanOut lowers a call on a loop element to a chain of branches, one per
// identity, each guarding the summarized call on that object. The element
// always names one of its identities, so the last branch falls through to
// next without a node.
func (e *extractor) fanOut(f *FanOut, c Callee, call *ast.CallExpr, s ast.Stmt, next int) (int, error) {
	if e.opts.ElementCall == nil || len(f.IDs) == 0 {
		return 0, e.diagnose(call, "call", "call on loop element "+f.Root+" has no per-object summary")
	}
	head := next
	for i := len(f.IDs) - 1; i >= 0; i-- {
		n, ok, err := e.opts.ElementCall(c, call, f.Root, f.IDs[i], e.expression)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, e.diagnose(call, "call", "call on loop element "+f.Root+" is not summarized for object "+f.IDs[i])
		}
		id, err := e.summarized(n, s, next)
		if err != nil {
			return 0, err
		}
		head = e.add(Node{Kind: "branch", Expression: expr.Expression(f.Root + " == " + strconv.Quote(f.IDs[i]))}, s, id, head)
	}
	return head, nil
}
