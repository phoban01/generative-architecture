package ir

// Node kinds in a process graph.
const (
	NodeBranch = "branch"
	NodeEffect = "effect"
	NodeAssign = "assign"
	NodeReturn = "return"
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
