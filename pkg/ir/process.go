package ir

// Node kinds in a process graph.
const (
	NodeBranch = "branch"
	NodeEffect = "effect"
	NodeAssign = "assign"
	NodeReturn = "return"
)

// Requeue requests of a return node, named as the runtime model names the
// outcomes they cause.
const (
	// RequeueImmediate is a rate-limited requeue: Result{Requeue: true}.
	RequeueImmediate = "requeue"
	// RequeueAfter is a delayed requeue: Result{RequeueAfter: d}.
	RequeueAfter = "after"
)

// Node is one step of a process. Branch nodes evaluate Expression and go to
// Next or Else; effect nodes perform the named Effect; return nodes end the
// process.
type Node struct {
	ID         string `yaml:"id"`
	Kind       string `yaml:"kind"`
	Effect     string `yaml:"effect,omitempty"`
	Expression string `yaml:"expression,omitempty"`
	Next       string `yaml:"next,omitempty"`
	Else       string `yaml:"else,omitempty"`
	Source     Source `yaml:"source,omitempty"`
	// Code is the Go source text of the statement or condition, when the
	// frontend keeps it.
	Code string `yaml:"code,omitempty"`
	// Requeue is the requeue a return node asks for: RequeueImmediate,
	// RequeueAfter or empty for none.
	Requeue string `yaml:"requeue,omitempty"`
}

// Process is the control-flow graph of one reconcile method.
type Process struct {
	ID         string     `yaml:"id"`
	Controller string     `yaml:"controller"`
	Method     string     `yaml:"method"`
	Entry      string     `yaml:"entry"`
	Nodes      []Node     `yaml:"nodes"`
	Provenance Provenance `yaml:"provenance"`
}
