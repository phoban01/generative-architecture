package architecture

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/sourcegen"

	"generative-architecture/pkg/expr"
	"go.yaml.in/yaml/v3"
)

type Expression = expr.Expression

type Local struct {
	Name  string
	Value Expression
}
type Locals []Local

func (l *Locals) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("locals must be a mapping")
	}
	for i := 0; i < len(n.Content); i += 2 {
		var e Expression
		if err := n.Content[i+1].Decode(&e); err != nil {
			return err
		}
		*l = append(*l, Local{n.Content[i].Value, e})
	}
	return nil
}

type Initializer struct {
	Domain string     `yaml:"domain"`
	Value  Expression `yaml:"value"`
}
type StateDef struct {
	Type string      `yaml:"type"`
	Init Initializer `yaml:"init"`
}
type Port struct {
	Direction string `yaml:"direction"`
	Protocol  string `yaml:"protocol"`
	Type      string `yaml:"type"`
	Key       string `yaml:"key"`
	State     string `yaml:"state,omitempty"`
}
type Update struct {
	Key   Expression  `yaml:"key"`
	Value Expression  `yaml:"value"`
	When  *Expression `yaml:"when,omitempty"`
}
type Action struct {
	Name     string            `yaml:"name"`
	Params   []string          `yaml:"params"`
	Guard    Expression        `yaml:"guard"`
	Updates  map[string]Update `yaml:"updates"`
	Accepted Expression        `yaml:"accepted"`
	Locals   Locals            `yaml:"locals,omitempty"`
}
type Contract struct {
	ID          string              `yaml:"-"`
	Version     string              `yaml:"version"`
	Title       string              `yaml:"title"`
	Origin      string              `yaml:"origin"`
	Description string              `yaml:"description"`
	State       map[string]StateDef `yaml:"state"`
	Ports       map[string]Port     `yaml:"ports"`
	Actions     []Action            `yaml:"actions"`
}
type ComponentRef struct {
	ID       string `yaml:"id"`
	Contract string `yaml:"contract"`
	Version  string `yaml:"version"`
}
type Consumer struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
}
type InfraMachine struct {
	ID       string `yaml:"id"`
	Consumer string `yaml:"consumer"`
}
type Binding struct {
	From     string `yaml:"from"`
	To       string `yaml:"to"`
	Evidence string `yaml:"evidence,omitempty"`
}
type Property struct {
	Name       string     `yaml:"name"`
	Expression Expression `yaml:"expression"`
}
type System struct {
	Composition         string            `yaml:"composition,omitempty"`
	Runtime             string            `yaml:"runtime,omitempty"`
	Sources             *sourcegen.Config `yaml:"sources,omitempty"`
	Name                string            `yaml:"title"`
	ExpressionLanguage  string            `yaml:"expressionLanguage"`
	Hosts               []string          `yaml:"hosts"`
	MaxEpoch            int64             `yaml:"maxEpoch"`
	AuthorizationPolicy string            `yaml:"authorizationPolicy"`
	Consumers           []Consumer        `yaml:"consumers"`
	InfraMachines       []InfraMachine    `yaml:"infraMachines"`
	Components          []ComponentRef    `yaml:"components"`
	Bindings            []Binding         `yaml:"bindings"`
	Properties          []Property        `yaml:"properties"`
	SafetyQuestions     []Property        `yaml:"safetyQuestions"`
}
type Step struct {
	Action string `yaml:"action"`
	Host   string `yaml:"host,omitempty"`
	Infra  string `yaml:"infra,omitempty"`
}
type LockEntry struct {
	Path    string `yaml:"path"`
	Version string `yaml:"version"`
	SHA256  string `yaml:"sha256"`
}
type Project struct {
	Sources   *sourcegen.Descriptor
	Root      string
	System    System
	Contracts map[string]Contract
	Scenarios map[string][]Step
	Lock      map[string]LockEntry
}

func keys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}
func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func unique(xs []string) bool {
	seen := map[string]bool{}
	for _, s := range xs {
		if s == "" || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}
func contains(xs []string, s string) bool {
	for _, v := range xs {
		if v == s {
			return true
		}
	}
	return false
}

// DecodeYAML remains available for strict CEL fixture decoding.
func DecodeYAML(b []byte, out any) error { return krm.DecodeYAML(b, out) }
func ReadResource[T any](path, kind string, out *T) error {
	_, err := krm.Read(path, kind, out)
	return err
}
func WriteResource(path, kind, name string, value any) error {
	return krm.Write(path, kind, name, value)
}
func ReadResult[T any](path, kind string, out *T) error { return krm.ReadResult(path, kind, out) }
func WriteResult(path, kind, name string, value any) error {
	return krm.WriteResult(path, kind, name, value)
}
func Load(root string) (*Project, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	p := &Project{Root: abs, Contracts: map[string]Contract{}, Lock: map[string]LockEntry{}}
	if err := ReadResource(filepath.Join(abs, "system.yaml"), "System", &p.System); err != nil {
		return nil, err
	}
	if err := ReadResource(filepath.Join(abs, "scenarios.yaml"), "ScenarioSuite", &p.Scenarios); err != nil {
		return nil, err
	}
	for _, ref := range p.System.Components {
		clean := filepath.Clean(ref.Contract)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("contract path escapes project")
		}
		var c Contract
		path := filepath.Join(abs, clean)
		meta, err := krm.Read(path, "Contract", &c)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ref.Contract, err)
		}
		c.ID = meta.Name
		if _, ok := p.Contracts[ref.ID]; ok {
			return nil, fmt.Errorf("duplicate component %s", ref.ID)
		}
		p.Contracts[ref.ID] = c
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		p.Lock[ref.ID] = LockEntry{ref.Contract, ref.Version, digest(b)}
	}
	return p, nil
}
