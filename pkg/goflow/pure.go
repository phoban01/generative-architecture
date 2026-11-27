package goflow

import "go/ast"

// Pure helpers compute values in memory and write no object the model
// tracks: sorting a local slice, formatting a string, comparing times,
// copying an object. A statement that only calls one has no model effect,
// and the frontend drops it as it drops a logging statement. In an
// expression a pure call still needs a value, which the frontend does not
// give it yet.

// purePackages hold only functions and methods with no model effect.
var purePackages = map[string]bool{
	"sort":                             true,
	"strconv":                          true,
	"strings":                          true,
	"k8s.io/client-go/tools/reference": true,
}

// pureFunctions are the pure functions of fmt, which also prints. A print
// is output, not a computation, and stays an unsummarized call.
var pureFunctions = map[Callee]bool{
	{Package: "fmt", Method: "Sprintf"}:  true,
	{Package: "fmt", Method: "Sprint"}:   true,
	{Package: "fmt", Method: "Sprintln"}: true,
	{Package: "fmt", Method: "Errorf"}:   true,
}

// blockingTime lists the functions of time that wait or start a timer.
// The rest of the package, time arithmetic and the clock, is pure.
var blockingTime = map[string]bool{
	"Sleep": true, "Tick": true, "After": true, "AfterFunc": true, "NewTimer": true, "NewTicker": true,
}

// metav1Package declares Time and MicroTime, whose methods compare times.
const metav1Package = "k8s.io/apimachinery/pkg/apis/meta/v1"

// pureMethods are the generated deep-copy methods that return a copy. The
// DeepCopyInto method writes its argument, so it is not one.
var pureMethods = map[string]bool{"DeepCopy": true, "DeepCopyObject": true}

// pureCallee reports whether c is a built-in pure helper; the extractor's
// method also accepts the callees Options.Pure declares.
func (e *extractor) pureCallee(c Callee) bool { return pureCallee(c) || e.opts.Pure[c] }

// pureCallee reports whether c has no model effect.
func pureCallee(c Callee) bool {
	if purePackages[c.Package] || pureFunctions[c] {
		return true
	}
	if c.Package == "time" {
		return c.Receiver != "" || !blockingTime[c.Method]
	}
	if c.Package == metav1Package && (c.Receiver == "Time" || c.Receiver == "MicroTime") {
		return true
	}
	return c.Receiver != "" && pureMethods[c.Method]
}

// pure reports whether s is a statement that only calls the pure helper c,
// such as sort.Slice(jobs, less). A call inside it that is not pure, a
// builtin or a conversion is a diagnostic, because dropping the statement
// would drop that call too.
func (e *extractor) pure(s ast.Stmt, call *ast.CallExpr, c Callee) (bool, error) {
	if _, ok := s.(*ast.ExprStmt); !ok || !e.pureCallee(c) {
		return false, nil
	}
	return true, e.innerCalls(call, e.pureCallee, "a pure helper call", "the statement")
}
