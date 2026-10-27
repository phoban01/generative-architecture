package composition

import (
	"fmt"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/protocol"
	"generative-architecture/pkg/sourcegen"
	"golang.org/x/mod/modfile"
	"os"
	"path/filepath"
)

// Projects are explicit local checkouts. The library neither fetches nor updates
// repositories implicitly; go.mod pins and file receipts make drift reviewable.
type Project struct {
	Root   string `yaml:"root"`
	Module string `yaml:"module"`
}
type ComponentRef struct {
	Name    string          `yaml:"name"`
	Project string          `yaml:"project,omitempty"`
	Model   string          `yaml:"model"`
	Ports   map[string]Port `yaml:"ports,omitempty"`
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
	Inputs map[string]string
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
	for _, ref := range doc.Components {
		dir, ok := roots[ref.Project]
		if !ok {
			return out, fmt.Errorf("unknown project %s", ref.Project)
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
	out.Result, err = ComposeWithProtocols(doc.Name, components, links, doc.Properties, protocols)
	return out, err
}
