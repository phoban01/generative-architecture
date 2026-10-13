package architecture

import (
	"fmt"
	"generative-architecture/pkg/sourcegen"
	"os"
	"path/filepath"
	"reflect"
)

// ExtractSources refreshes checked structure without rewriting behavioral contracts.
func ExtractSources(root string) error {
	var s System
	if err := ReadResource(filepath.Join(root, "system.yaml"), "System", &s); err != nil {
		return err
	}
	if s.Sources == nil {
		return nil
	}
	path, err := sourcegen.LocalPath(root, s.Sources.Descriptor)
	if err != nil {
		return err
	}
	// Never leave a prior descriptor looking current after a failed extraction.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	d, err := sourcegen.Extract(root, *s.Sources)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return WriteResult(path, "SourceDescriptor", "sources", d)
}
func (p *Project) ValidateSources() error {
	if p.System.Sources == nil {
		return nil
	}
	path, err := sourcegen.LocalPath(p.Root, p.System.Sources.Descriptor)
	if err != nil {
		return err
	}
	var d sourcegen.Descriptor
	if err := ReadResult(path, "SourceDescriptor", &d); err != nil {
		return fmt.Errorf("read source descriptor (run arch extract): %w", err)
	}
	if err := d.Verify(p.Root, *p.System.Sources); err != nil {
		return err
	}
	for name, fields := range d.Records {
		if !reflect.DeepEqual(domainExpressions.Records[name], fields) {
			return fmt.Errorf("source record %s incompatible with machine-vendor contract", name)
		}
	}
	seen := map[string]bool{}
	for ci := range d.Controllers {
		ctrl := &d.Controllers[ci]
		c, ok := p.Contracts[ctrl.Component]
		if !ok {
			return fmt.Errorf("source controller component %s missing from system", ctrl.Component)
		}
		if seen[ctrl.Component] {
			return fmt.Errorf("duplicate source component %s", ctrl.Component)
		}
		seen[ctrl.Component] = true
		ctrl.Contract, ctrl.Version = p.Lock[ctrl.Component].Path, c.Version
		if len(ctrl.Ports) != len(c.Ports) {
			return fmt.Errorf("source ports incomplete for %s", ctrl.Component)
		}
		ports := map[string]bool{}
		for pi := range ctrl.Ports {
			port := &ctrl.Ports[pi]
			target, ok := c.Ports[port.Name]
			if !ok || ports[port.Name] || target.Type != port.Type {
				return fmt.Errorf("source port %s.%s incompatible with contract (extracted %s, contract %s)", ctrl.Component, port.Name, port.Type, target.Type)
			}
			ports[port.Name] = true
			port.Direction, port.Protocol, port.Key = target.Direction, target.Protocol, target.Key
		}
		if len(ctrl.Actions) != len(c.Actions) {
			return fmt.Errorf("source actions incomplete for %s", ctrl.Component)
		}
		names := map[string]bool{}
		for _, a := range c.Actions {
			names[a.Name] = true
		}
		for _, a := range ctrl.Actions {
			if !names[a.Name] {
				return fmt.Errorf("unknown/duplicate source action %s.%s", ctrl.Component, a.Name)
			}
			delete(names, a.Name)
		}
	}
	p.Sources = &d
	return nil
}
