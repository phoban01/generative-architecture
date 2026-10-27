// Package boundary defines BoundaryModel, a declared model of an upstream
// project that is not extracted. A boundary model pins the upstream version,
// lists the resources the project owns, watches and writes, the ports it
// offers to a composition, and the assumptions it rests on. Each assumption
// carries an evidence kind and a conformance hook that says how it could be
// checked later. A boundary never claims extracted or observed evidence.
package boundary

import (
	"errors"
	"fmt"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/protocol"
	"generative-architecture/pkg/sourcegen"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"golang.org/x/mod/semver"
)

// Kind is the KRM kind of a boundary model document.
const Kind = "BoundaryModel"

// Generator names this package in the provenance of facts it produces.
const Generator = "boundary"

// Conformance hook kinds: how an assumption could be checked later.
const (
	ConformanceEnvtest       = "envtest"
	ConformanceDocumentation = "documentation"
	ConformanceNone          = "none"
)

// Conformance says how an assumption could be checked against the upstream
// project. Reference names the envtest or the documentation page. A hook of
// kind none has no reference.
type Conformance struct {
	Kind      string `yaml:"kind"`
	Reference string `yaml:"reference,omitempty"`
}

// Assumption is one statement about upstream behaviour that the model rests
// on. Evidence is declared (stated by upstream documentation) or assumed
// (believed but not documented). Reference points at the source of the
// statement.
type Assumption struct {
	ID          string      `yaml:"id"`
	Statement   string      `yaml:"statement"`
	Evidence    ir.Evidence `yaml:"evidence"`
	Reference   string      `yaml:"reference,omitempty"`
	Conformance Conformance `yaml:"conformance"`
}

// Watch is one resource the upstream project watches. Cluster is the cluster
// the watched objects live in; empty means the boundary's own cluster.
type Watch struct {
	ID       string       `yaml:"id"`
	Resource ir.GVK       `yaml:"resource"`
	Cluster  ir.ClusterID `yaml:"cluster,omitempty"`
}

// Effect is one API operation the upstream project performs. Cluster is the
// cluster the operation targets; empty means the boundary's own cluster.
type Effect struct {
	ID          string            `yaml:"id"`
	Resource    ir.GVK            `yaml:"resource"`
	Operation   string            `yaml:"operation"`
	Consistency string            `yaml:"consistency"`
	Subresource string            `yaml:"subresource,omitempty"`
	Options     map[string]string `yaml:"options,omitempty"`
	Cluster     ir.ClusterID      `yaml:"cluster,omitempty"`
}

// Port is one port the boundary offers to a composition. A port names either
// a Resource, whose contract is the resource contract of its GVK, or an
// authored Contract with a Version. State is the model state variable that
// carries the port.
type Port struct {
	State     string  `yaml:"state"`
	Resource  *ir.GVK `yaml:"resource,omitempty"`
	Contract  string  `yaml:"contract,omitempty"`
	Version   string  `yaml:"version,omitempty"`
	Direction string  `yaml:"direction"`
	Writable  bool    `yaml:"writable,omitempty"`
}

// Spec is the payload of a BoundaryModel document. Upstream is the module
// path or image repository and Version its pinned semantic version. Digest
// optionally pins an image as sha256:<hex>. Model and Contracts are paths
// relative to the document: a TransitionModel fragment for the boundary's
// behaviour and ProtocolContract documents its ports follow.
type Spec struct {
	Upstream    string          `yaml:"upstream"`
	Version     string          `yaml:"version"`
	Digest      string          `yaml:"digest,omitempty"`
	Cluster     ir.ClusterID    `yaml:"cluster,omitempty"`
	Model       string          `yaml:"model,omitempty"`
	Contracts   []string        `yaml:"contracts,omitempty"`
	Owns        []ir.GVK        `yaml:"owns,omitempty"`
	Watches     []Watch         `yaml:"watches,omitempty"`
	Effects     []Effect        `yaml:"effects,omitempty"`
	Ports       map[string]Port `yaml:"ports,omitempty"`
	Assumptions []Assumption    `yaml:"assumptions,omitempty"`
}

// Boundary is a loaded and validated boundary model.
type Boundary struct {
	Name string
	Path string
	// SHA256 is the hash of the boundary document. The document holds the
	// pinned version, so the hash pins it too.
	SHA256    string
	Spec      Spec
	Model     *model.Spec
	Contracts []protocol.Contract
	// Files maps every file read, the document first among them, to its hash.
	Files map[string]string
}

var (
	identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	digest     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	writeOps   = map[string]bool{ir.OpCreate: true, ir.OpUpdate: true, ir.OpPatch: true, ir.OpDelete: true, ir.OpDeleteAllOf: true, ir.OpApply: true}
)

// Load reads a BoundaryModel document strictly, then its model and
// contracts, and validates them together. Errors start with the file and name
// the spec field at fault.
func Load(path string) (Boundary, error) {
	b := Boundary{Path: path, Files: map[string]string{}}
	record := func(file string) error {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		b.Files[file] = goflow.Hash(data)
		return nil
	}
	meta, err := krm.Read(path, Kind, &b.Spec)
	if err != nil {
		return b, err
	}
	b.Name = meta.Name
	if err = record(path); err != nil {
		return b, err
	}
	b.SHA256 = b.Files[path]
	dir := filepath.Dir(path)
	if b.Spec.Model != "" {
		file, err := sourcegen.LocalPath(dir, b.Spec.Model)
		if err != nil {
			return b, fmt.Errorf("%s: spec.model: %w", path, err)
		}
		var m model.Spec
		if _, err = krm.Read(file, "TransitionModel", &m); err != nil {
			return b, fmt.Errorf("%s: spec.model: %w", path, err)
		}
		if _, err = model.Compile(m); err != nil {
			return b, fmt.Errorf("%s: spec.model: %s: %w", path, file, err)
		}
		if err = record(file); err != nil {
			return b, err
		}
		b.Model = &m
	}
	for i, c := range b.Spec.Contracts {
		file, err := sourcegen.LocalPath(dir, c)
		if err != nil {
			return b, fmt.Errorf("%s: spec.contracts[%d]: %w", path, i, err)
		}
		contract, err := protocol.Load(file)
		if err != nil {
			return b, fmt.Errorf("%s: spec.contracts[%d]: %w", path, i, err)
		}
		if err = record(file); err != nil {
			return b, err
		}
		b.Contracts = append(b.Contracts, contract)
	}
	if err = b.Validate(); err != nil {
		return b, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// Validate checks the spec and, when a model is loaded, that every port
// state is a model state variable.
func (b Boundary) Validate() error {
	errs := []error{b.Spec.Validate()}
	if b.Model != nil {
		for _, name := range sortedKeys(b.Spec.Ports) {
			p := b.Spec.Ports[name]
			if _, ok := b.Model.State[p.State]; !ok {
				errs = append(errs, fmt.Errorf("spec.ports.%s.state: %q is not a state variable of model %s", name, p.State, b.Model.Name))
			}
		}
	} else if len(b.Spec.Ports) > 0 {
		errs = append(errs, fmt.Errorf("spec.ports: ports need a model that holds their state; set spec.model"))
	}
	return errors.Join(errs...)
}

// Validate checks the spec on its own and reports every problem found. Each
// error names the spec field at fault.
func (s Spec) Validate() error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if s.Upstream == "" {
		fail("spec.upstream: required")
	}
	if s.Version == "" {
		fail("spec.version: required; pin the upstream release")
	} else if !semver.IsValid(s.Version) {
		fail("spec.version: %q is not a semantic version such as v1.2.3", s.Version)
	}
	if s.Digest != "" && !digest.MatchString(s.Digest) {
		fail("spec.digest: %q is not sha256:<64 lowercase hex digits>", s.Digest)
	}
	if len(s.Assumptions) == 0 && len(s.Contracts) == 0 {
		fail("spec: at least one assumption or contract is required")
	}
	ids := map[string]bool{}
	for i, a := range s.Assumptions {
		at := fmt.Sprintf("spec.assumptions[%d]", i)
		if !identifier.MatchString(a.ID) || ids[a.ID] {
			fail("%s.id: %q is not an identifier or is a duplicate", at, a.ID)
		}
		ids[a.ID] = true
		if a.Statement == "" {
			fail("%s.statement: required", at)
		}
		if err := a.Evidence.Validate(); err != nil {
			fail("%s.evidence: %w", at, err)
		} else if a.Evidence != ir.Declared && a.Evidence != ir.Assumed {
			fail("%s.evidence: a boundary is not extracted or observed; evidence must be declared or assumed, got %s", at, a.Evidence)
		}
		switch a.Conformance.Kind {
		case ConformanceEnvtest, ConformanceDocumentation:
			if a.Conformance.Reference == "" {
				fail("%s.conformance.reference: required for a %s hook", at, a.Conformance.Kind)
			}
		case ConformanceNone:
			if a.Conformance.Reference != "" {
				fail("%s.conformance.reference: a hook of kind none has no reference", at)
			}
		default:
			fail("%s.conformance.kind: unknown kind %q; expected envtest, documentation or none", at, a.Conformance.Kind)
		}
	}
	for i, g := range s.Owns {
		if err := g.Validate(); err != nil {
			fail("spec.owns[%d]: %w", i, err)
		}
	}
	ids = map[string]bool{}
	for i, w := range s.Watches {
		at := fmt.Sprintf("spec.watches[%d]", i)
		if !identifier.MatchString(w.ID) || ids[w.ID] {
			fail("%s.id: %q is not an identifier or is a duplicate", at, w.ID)
		}
		ids[w.ID] = true
		if err := w.Resource.Validate(); err != nil {
			fail("%s.resource: %w", at, err)
		}
	}
	ids = map[string]bool{}
	for i, e := range s.Effects {
		at := fmt.Sprintf("spec.effects[%d]", i)
		if !identifier.MatchString(e.ID) || ids[e.ID] {
			fail("%s.id: %q is not an identifier or is a duplicate", at, e.ID)
		}
		ids[e.ID] = true
		if err := e.IR("boundary", ir.Provenance{}).Validate(); err != nil {
			fail("%s: %w", at, err)
		}
	}
	for _, name := range sortedKeys(s.Ports) {
		errs = append(errs, s.validatePort(name, s.Ports[name]))
	}
	return errors.Join(errs...)
}

// validatePort checks one port. A resource port must be backed by the
// boundary's own facts in its own cluster: an out port by a write effect, an
// in port by a watch or a read effect, and a writable in port also by a write
// effect. Cross-cluster facts never back a resource port, so a binding across
// clusters always uses an authored contract and an explicit link.
func (s Spec) validatePort(name string, p Port) error {
	at := "spec.ports." + name
	if !identifier.MatchString(name) {
		return fmt.Errorf("%s: %q is not an identifier", at, name)
	}
	if p.State == "" {
		return fmt.Errorf("%s.state: required", at)
	}
	if p.Direction != "in" && p.Direction != "out" {
		return fmt.Errorf("%s.direction: unknown direction %q; expected in or out", at, p.Direction)
	}
	if p.Direction == "out" && p.Writable {
		return fmt.Errorf("%s.writable: only an in port is writable", at)
	}
	if p.Resource == nil {
		if p.Contract == "" || p.Version == "" {
			return fmt.Errorf("%s: a port names a resource, or a contract and a version", at)
		}
		return nil
	}
	if p.Contract != "" || p.Version != "" {
		return fmt.Errorf("%s: a resource port takes its contract from the resource; remove contract and version", at)
	}
	if err := p.Resource.Validate(); err != nil {
		return fmt.Errorf("%s.resource: %w", at, err)
	}
	home := func(c ir.ClusterID) bool { return c == "" || c == s.Cluster }
	var read, write bool
	for _, w := range s.Watches {
		read = read || (w.Resource == *p.Resource && home(w.Cluster))
	}
	for _, e := range s.Effects {
		if e.Resource != *p.Resource || !home(e.Cluster) || e.Consistency == ir.ConsistencySchema {
			continue
		}
		if writeOps[e.Operation] {
			write = true
		} else {
			read = true
		}
	}
	switch {
	case p.Direction == "out" && !write:
		return fmt.Errorf("%s: out port for %s has no write effect in the boundary's cluster", at, p.Resource)
	case p.Direction == "in" && !read:
		return fmt.Errorf("%s: in port for %s has no watch or read effect in the boundary's cluster", at, p.Resource)
	case p.Writable && !write:
		return fmt.Errorf("%s: writable in port for %s has no write effect in the boundary's cluster", at, p.Resource)
	}
	return nil
}

// IR converts the effect to an IR api effect of actor. Its evidence is
// assumed: the upstream code is never extracted.
func (e Effect) IR(actor string, p ir.Provenance) ir.Effect {
	return ir.Effect{ID: e.ID, Kind: ir.KindAPI, Actor: actor, Cluster: e.Cluster, Resource: e.Resource, Operation: e.Operation, Consistency: e.Consistency, Subresource: e.Subresource, Options: e.Options, Evidence: ir.Assumed, Provenance: p}
}

// IR converts the watch to an IR watch of controller. Its evidence is
// assumed and its key mapping is custom: the upstream mapper is not
// summarized.
func (w Watch) IR(controller string, p ir.Provenance) ir.Watch {
	return ir.Watch{ID: w.ID, Controller: controller, Cluster: w.Cluster, Resource: w.Resource, Mapping: ir.KeyMapping{Kind: ir.MappingCustom}, Evidence: ir.Assumed, Provenance: p}
}

// Facts returns the boundary's watches and effects as IR facts of actor, all
// with assumed evidence and the boundary's provenance. A fact without a
// cluster gets the boundary's cluster.
func (b Boundary) Facts(actor string) ([]ir.Watch, []ir.Effect) {
	p := b.Provenance()
	var watches []ir.Watch
	for _, w := range b.Spec.Watches {
		if w.Cluster == "" {
			w.Cluster = b.Spec.Cluster
		}
		watches = append(watches, w.IR(actor, p))
	}
	var effects []ir.Effect
	for _, e := range b.Spec.Effects {
		if e.Cluster == "" {
			e.Cluster = b.Spec.Cluster
		}
		effects = append(effects, e.IR(actor, p))
	}
	return watches, effects
}

// Provenance is the provenance of every fact taken from the boundary: the
// upstream module and version and the hash of the document.
func (b Boundary) Provenance() ir.Provenance {
	return ir.Provenance{Module: b.Spec.Upstream, ModuleVersion: b.Spec.Version, Source: ir.Source{File: b.Path, SHA256: b.SHA256}, Generator: Generator}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
