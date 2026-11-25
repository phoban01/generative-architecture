package composition

import (
	"fmt"
	"github.com/phoban01/generative-architecture/pkg/boundary"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/protocol"
	"github.com/phoban01/generative-architecture/pkg/sourcegen"
	"golang.org/x/mod/modfile"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Projects are explicit local checkouts. The library neither fetches nor updates
// repositories implicitly; go.mod pins and file receipts make drift reviewable.
type Project struct {
	Root   string `yaml:"root"`
	Module string `yaml:"module"`
}

// ComponentRef names one component: a TransitionModel with declared ports,
// or a BoundaryModel that brings its own model, ports and facts. Exactly one
// of Model and Boundary is set. Both paths are relative to the project.
//
// IR names an ArchitectureIR document, relative to the project, that holds
// the facts of a model component. Its watches and api effects derive the
// component's resource ports (ADR-0010), and Ports adds declared ports to
// them. Controller selects the controller of the document by ID or name;
// it may be empty when the document holds one controller. Cluster places
// the component in a cluster for the resource joins; empty is the implicit
// cluster.
type ComponentRef struct {
	Name       string          `yaml:"name"`
	Project    string          `yaml:"project,omitempty"`
	Model      string          `yaml:"model,omitempty"`
	Boundary   string          `yaml:"boundary,omitempty"`
	IR         string          `yaml:"ir,omitempty"`
	Controller string          `yaml:"controller,omitempty"`
	Cluster    ir.ClusterID    `yaml:"cluster,omitempty"`
	Ports      map[string]Port `yaml:"ports,omitempty"`
	// Applies names, per out port of a boundary with an authored contract,
	// the kinds the upstream project applies in this system. The kinds of
	// an applied manifest depend on its content, so the boundary cannot
	// name them. Each kind becomes an assumed server-side apply effect of
	// the component and a derived resource out port on the same state, so
	// the applied objects join by resource.
	Applies map[string][]ir.GVK `yaml:"applies,omitempty"`
}
type Document struct {
	Name       string             `yaml:"name"`
	AutoLink   bool               `yaml:"autoLink,omitempty"`
	Projects   map[string]Project `yaml:"projects,omitempty"`
	Components []ComponentRef     `yaml:"components"`
	Links      []Link             `yaml:"links"`
	Properties []model.Property   `yaml:"properties"`
}
type Loaded struct {
	Result Result
	// Components are the loaded components in document order, with their
	// evidence and IR facts. When a component derives its ports from an IR
	// document, ResolveProviders has chosen the provider of each resource.
	Components []Component
	// Links are the explicit links followed by the derived ones.
	Links []Link
	// Diagnostics are the duplicate-port and join findings. Load returns
	// an error with them when any is present, because an unresolved or
	// ambiguous provider leaves an input unbound. Result is then empty.
	Diagnostics []Diagnostic
	Inputs      map[string]string
}

func Load(root, path string) (Loaded, error) {
	out := Loaded{Inputs: map[string]string{}}
	var doc Document
	full, err := sourcegen.LocalPath(root, path)
	if err != nil {
		return out, err
	}
	if _, err = krm.Read(full, "SystemComposition", &doc); err != nil {
		return out, err
	}
	record := func(path string) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		out.Inputs[abs] = goflow.Hash(b)
		return nil
	}
	if err = record(full); err != nil {
		return out, err
	}
	roots := map[string]string{"": root}
	for name, p := range doc.Projects {
		if name == "" || p.Root == "" || p.Module == "" {
			return out, fmt.Errorf("project name/root/module required")
		}
		dir := p.Root
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			return out, err
		}
		f, err := modfile.Parse("go.mod", b, nil)
		if err != nil {
			return out, err
		}
		if f.Module == nil || f.Module.Mod.Path != p.Module {
			return out, fmt.Errorf("project %s module mismatch", name)
		}
		roots[name] = dir
		for _, file := range []string{"go.mod", "go.sum"} {
			if err = record(filepath.Join(dir, file)); err != nil {
				if file == "go.sum" && os.IsNotExist(err) {
					continue
				}
				return out, err
			}
		}
	}
	components := []Component{}
	cluster := map[string]ir.ClusterID{}
	var diags []Diagnostic
	derived := false
	for i, ref := range doc.Components {
		dir, ok := roots[ref.Project]
		if !ok {
			return out, fmt.Errorf("unknown project %s", ref.Project)
		}
		at := fmt.Sprintf("%s: spec.components[%d] (%s)", full, i, ref.Name)
		if ref.Cluster != "" {
			cluster[ref.Name] = ref.Cluster
		}
		if len(ref.Applies) > 0 && ref.Boundary == "" {
			return out, fmt.Errorf("%s: applies: only a boundary component applies kinds", at)
		}
		if ref.Boundary != "" {
			if ref.IR != "" {
				return out, fmt.Errorf("%s: ir and boundary are exclusive; the boundary brings its facts", at)
			}
			c, files, err := LoadBoundary(dir, ref)
			if err != nil {
				return out, fmt.Errorf("%s: %w", at, err)
			}
			for _, file := range files {
				if err = record(file); err != nil {
					return out, err
				}
			}
			components = append(components, c)
			continue
		}
		if ref.Model == "" {
			return out, fmt.Errorf("%s: model or boundary required", at)
		}
		file, err := sourcegen.LocalPath(dir, ref.Model)
		if err != nil {
			return out, err
		}
		var spec model.Spec
		if _, err = krm.Read(file, "TransitionModel", &spec); err != nil {
			return out, err
		}
		if err = record(file); err != nil {
			return out, err
		}
		c := Component{Name: ref.Name, Model: spec, Ports: ref.Ports}
		if ref.IR != "" {
			facts, err := loadFacts(dir, ref)
			if err != nil {
				return out, fmt.Errorf("%s: %w", at, err)
			}
			if err = record(facts.file); err != nil {
				return out, err
			}
			c.Watches, c.Effects, c.Evidence = facts.watches, facts.effects, facts.evidence
			var ds []Diagnostic
			c.Ports, ds = MergePorts(ref.Name, ref.Ports, PortsFromIR(ref.Name, c.Watches, c.Effects))
			diags = append(diags, ds...)
			derived = true
		}
		components = append(components, c)
	}
	links := doc.Links
	if derived {
		// Derived resource ports join by resource identity; the provider
		// of each resource is its owner, or else its sole creator.
		joined, ds, err := JoinByResource(components, cluster, links)
		if err != nil {
			return out, err
		}
		components = ResolveProviders(components, cluster)
		links = joined
		diags = append(diags, ds...)
	}
	out.Components, out.Links, out.Diagnostics = components, links, diags
	if len(diags) > 0 {
		return out, fmt.Errorf("%s: %d port findings, first %s", full, len(diags), diags[0])
	}
	if doc.AutoLink {
		links, err = AutoLink(components, links)
		if err != nil {
			return out, err
		}
		out.Links = links
	}
	// A link's protocol is a ProtocolContract file relative to root.
	protocols := map[string]protocol.Contract{}
	for i, l := range links {
		if l.Protocol == "" || protocols[l.Protocol].Name != "" {
			continue
		}
		file, err := sourcegen.LocalPath(root, l.Protocol)
		if err != nil {
			return out, fmt.Errorf("links[%d].protocol: %w", i, err)
		}
		c, err := protocol.Load(file)
		if err != nil {
			return out, fmt.Errorf("links[%d].protocol: %w", i, err)
		}
		if err = record(file); err != nil {
			return out, err
		}
		protocols[l.Protocol] = c
	}
	out.Result, err = ComposeWithProtocols(doc.Name, components, links, doc.Properties, protocols)
	return out, err
}

// facts are the IR facts of one controller: the file they came from, its
// watches and api effects, and the component evidence they support.
type facts struct {
	file     string
	watches  []ir.Watch
	effects  []ir.Effect
	evidence ir.Evidence
}

// loadFacts reads the ArchitectureIR document ref.IR names and selects the
// controller ref.Controller names by ID or name, or the only controller
// when ref.Controller is empty. The component's evidence is the weakest
// evidence of its facts: a component is known no better than its least
// known fact. A controller without facts leaves the evidence empty.
func loadFacts(dir string, ref ComponentRef) (facts, error) {
	var out facts
	file, err := sourcegen.LocalPath(dir, ref.IR)
	if err != nil {
		return out, fmt.Errorf("ir: %w", err)
	}
	doc, err := ir.Read(file)
	if err != nil {
		return out, fmt.Errorf("ir: %w", err)
	}
	out.file = file
	var selected *ir.Controller
	for i := range doc.Controllers {
		c := &doc.Controllers[i]
		if ref.Controller != "" && c.ID != ref.Controller && c.Name != ref.Controller {
			continue
		}
		if selected != nil {
			ids := make([]string, 0, len(doc.Controllers))
			for _, c := range doc.Controllers {
				ids = append(ids, c.ID)
			}
			return out, fmt.Errorf("ir %s holds %d controllers; set controller to one of %s", ref.IR, len(doc.Controllers), strings.Join(ids, ", "))
		}
		selected = c
	}
	if selected == nil {
		if ref.Controller == "" {
			return out, fmt.Errorf("ir %s holds no controller", ref.IR)
		}
		return out, fmt.Errorf("ir %s holds no controller %q", ref.IR, ref.Controller)
	}
	for _, w := range doc.Watches {
		if w.Controller == selected.ID {
			out.watches = append(out.watches, w)
			out.evidence = weakest(out.evidence, w.Evidence)
		}
	}
	for _, e := range doc.Effects {
		if e.Actor == selected.ID {
			out.effects = append(out.effects, e)
			out.evidence = weakest(out.evidence, e.Evidence)
		}
	}
	return out, nil
}

// weakest returns the lower-ranked of a and b; an empty a yields b.
func weakest(a, b ir.Evidence) ir.Evidence {
	if a == "" {
		return b
	}
	if ir.Strongest(a, b) == a {
		return b
	}
	return a
}

// LoadBoundary loads the BoundaryModel a component names. The component's
// model is the boundary's TransitionModel fragment. A boundary never
// contributes an extracted process. Its ports, watches and effects are
// assumed. A resource port gets the resource contract of its GVK, so it joins
// like a derived port. The kinds ref.Applies names become apply effects and
// resource out ports. LoadBoundary returns every file it read.
func LoadBoundary(dir string, ref ComponentRef) (Component, []string, error) {
	if ref.Model != "" {
		return Component{}, nil, fmt.Errorf("model and boundary are exclusive; the boundary names its model")
	}
	if len(ref.Ports) > 0 {
		return Component{}, nil, fmt.Errorf("ports: a boundary component takes its ports from the boundary document")
	}
	file, err := sourcegen.LocalPath(dir, ref.Boundary)
	if err != nil {
		return Component{}, nil, fmt.Errorf("boundary: %w", err)
	}
	b, err := boundary.Load(file)
	if err != nil {
		return Component{}, nil, err
	}
	if b.Model == nil {
		return Component{}, nil, fmt.Errorf("boundary %s: a boundary component needs spec.model; a boundary without a model describes but does not compose", file)
	}
	c := Component{Name: ref.Name, Model: *b.Model, Ports: map[string]Port{}, Evidence: ir.Assumed}
	c.Watches, c.Effects = b.Facts(ref.Name)
	for name, p := range b.Spec.Ports {
		port := Port{State: p.State, Contract: p.Contract, Version: p.Version, Direction: p.Direction, Writable: p.Writable, Evidence: ir.Assumed, Provenance: []ir.Provenance{b.Provenance()}}
		if p.Resource != nil {
			port.Contract, port.Version = ResourceContract(*p.Resource), ResourceContractVersion
		}
		c.Ports[name] = port
	}
	if err := applyKinds(&c, b, ref.Applies); err != nil {
		return Component{}, nil, err
	}
	files := make([]string, 0, len(b.Files))
	for f := range b.Files {
		files = append(files, f)
	}
	sort.Strings(files)
	return c, files, nil
}

// applyKinds adds, for each kind applies names under an authored out port
// of the boundary, an assumed server-side apply effect and a resource out
// port on the same state, marked Creator so the ownership rule applies.
func applyKinds(c *Component, b boundary.Boundary, applies map[string][]ir.GVK) error {
	names := make([]string, 0, len(applies))
	for name := range applies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p, ok := b.Spec.Ports[name]
		if !ok {
			return fmt.Errorf("applies.%s: the boundary has no port %s", name, name)
		}
		if p.Direction != "out" || p.Resource != nil {
			return fmt.Errorf("applies.%s: applies names the kinds behind an out port with an authored contract; port %s is %s with contract %s", name, name, p.Direction, c.Ports[name].Contract)
		}
		for _, g := range applies[name] {
			if g.Version == "" || g.Kind == "" {
				return fmt.Errorf("applies.%s: a kind needs version and kind, got %+v", name, g)
			}
			portID := portName(g)
			if _, taken := c.Ports[portID]; taken {
				return fmt.Errorf("applies.%s: port %s exists", name, portID)
			}
			effect := ir.Effect{ID: name + g.Kind, Kind: ir.KindAPI, Actor: c.Name, Cluster: b.Spec.Cluster, Resource: g, Operation: ir.OpApply, Consistency: ir.ConsistencyDirect, Evidence: ir.Assumed, Provenance: b.Provenance()}
			if err := effect.Validate(); err != nil {
				return fmt.Errorf("applies.%s: %w", name, err)
			}
			c.Effects = append(c.Effects, effect)
			c.Ports[portID] = Port{State: p.State, Contract: ResourceContract(g), Version: ResourceContractVersion, Direction: "out", Creator: true, Evidence: ir.Assumed, Provenance: []ir.Provenance{b.Provenance()}}
		}
	}
	return nil
}
