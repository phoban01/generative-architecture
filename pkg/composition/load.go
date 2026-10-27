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
type ComponentRef struct {
	Name     string          `yaml:"name"`
	Project  string          `yaml:"project,omitempty"`
	Model    string          `yaml:"model,omitempty"`
	Boundary string          `yaml:"boundary,omitempty"`
	Ports    map[string]Port `yaml:"ports,omitempty"`
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
	// evidence and IR facts.
	Components []Component
	Inputs     map[string]string
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
	for i, ref := range doc.Components {
		dir, ok := roots[ref.Project]
		if !ok {
			return out, fmt.Errorf("unknown project %s", ref.Project)
		}
		if ref.Boundary != "" {
			at := fmt.Sprintf("%s: spec.components[%d] (%s)", full, i, ref.Name)
			c, files, err := loadBoundary(dir, ref)
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
		components = append(components, Component{Name: ref.Name, Model: spec, Ports: ref.Ports})
	}
	links := doc.Links
	if doc.AutoLink {
		links, err = AutoLink(components, links)
		if err != nil {
			return out, err
		}
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
	out.Components = components
	out.Result, err = ComposeWithProtocols(doc.Name, components, links, doc.Properties, protocols)
	return out, err
}

// loadBoundary loads the BoundaryModel a component names. The component's
// model is the boundary's TransitionModel fragment. A boundary never
// contributes an extracted process. Its ports, watches and effects are
// assumed. A resource port gets the resource contract of its GVK, so it joins
// like a derived port. loadBoundary returns every file it read.
func loadBoundary(dir string, ref ComponentRef) (Component, []string, error) {
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
	files := make([]string, 0, len(b.Files))
	for f := range b.Files {
		files = append(files, f)
	}
	sort.Strings(files)
	return c, files, nil
}
