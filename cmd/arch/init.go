package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/project"
)

// Files written by arch init into the current directory.
const (
	projectFile     = "project.yaml"
	constraintsFile = "constraints.yaml"
)

// constraintsKind is the KRM kind of an authored constraints document.
const constraintsKind = "ModelConstraints"

// stringList collects a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// runInit writes a starter project document and an empty constraints
// document into the current directory. It refuses to replace either file
// unless --force is given.
func runInit(g globals, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	module := fs.String("module", "", "Go module path the project analyses (required)")
	name := fs.String("name", "", "project name (default: the current directory name)")
	force := fs.Bool("force", false, "replace existing files")
	var pkgs, crds, discovery stringList
	fs.Var(&pkgs, "package", "Go package pattern relative to the module root, such as ./... (repeatable; default ./...)")
	fs.Var(&crds, "crd", "CRD manifest file, or a directory of them, relative to the current directory (repeatable)")
	fs.Var(&discovery, "discovery", "discovery snapshot file relative to the current directory (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("arch init: unexpected arguments %v", fs.Args())
	}
	if *module == "" {
		return fmt.Errorf("arch init: --module is required")
	}
	if len(pkgs) == 0 {
		pkgs = stringList{"./..."}
	}
	if *name == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		*name = projectName(filepath.Base(wd))
	}
	if errs := validation.IsDNS1123Subdomain(*name); len(errs) > 0 {
		return fmt.Errorf("arch init: invalid name %q: %s", *name, strings.Join(errs, "; "))
	}
	catalog, err := catalogSection(crds, discovery)
	if err != nil {
		return fmt.Errorf("arch init: %w", err)
	}
	spec := project.Spec{
		Module:      *module,
		Packages:    pkgs,
		Catalog:     catalog,
		Constraints: []string{constraintsFile},
		Runtime:     project.Runtime{Profile: "controller-runtime"},
	}
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("arch init: %w", err)
	}
	if !*force {
		var existing []string
		for _, f := range []string{projectFile, constraintsFile} {
			if _, err := os.Stat(f); err == nil {
				existing = append(existing, f)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if len(existing) > 0 {
			return fmt.Errorf("arch init: %s already exists; use --force to replace", strings.Join(existing, " and "))
		}
	}
	constraints := struct {
		Properties []any `yaml:"properties"`
	}{Properties: []any{}}
	if err := krm.Write(constraintsFile, constraintsKind, *name, constraints); err != nil {
		return fmt.Errorf("arch init: %w", err)
	}
	if err := writeProject(*name, spec); err != nil {
		return fmt.Errorf("arch init: %w", err)
	}
	return nil
}

var notName = regexp.MustCompile(`[^a-z0-9]+`)

// projectName turns a directory name into a DNS-1123 name.
func projectName(dir string) string {
	n := strings.Trim(notName.ReplaceAllString(strings.ToLower(dir), "-"), "-")
	if n == "" {
		return "project"
	}
	return n
}

// catalogSection builds the catalog section from the --crd and --discovery
// flags. A --crd directory contributes its .yaml, .yml and .json files in
// name order. Every path is checked to exist and recorded as given,
// relative to the project document.
func catalogSection(crds, discovery []string) (project.Catalog, error) {
	var c project.Catalog
	for _, p := range crds {
		info, err := os.Stat(p)
		if err != nil {
			return c, fmt.Errorf("--crd %s: %w", p, err)
		}
		if !info.IsDir() {
			c.CRDs = append(c.CRDs, filepath.ToSlash(p))
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return c, fmt.Errorf("--crd %s: %w", p, err)
		}
		n := 0
		for _, e := range entries {
			ext := filepath.Ext(e.Name())
			if e.IsDir() || (ext != ".yaml" && ext != ".yml" && ext != ".json") {
				continue
			}
			c.CRDs = append(c.CRDs, filepath.ToSlash(filepath.Join(p, e.Name())))
			n++
		}
		if n == 0 {
			return c, fmt.Errorf("--crd %s: directory holds no .yaml, .yml or .json file", p)
		}
	}
	for _, p := range discovery {
		info, err := os.Stat(p)
		if err != nil {
			return c, fmt.Errorf("--discovery %s: %w", p, err)
		}
		if info.IsDir() {
			return c, fmt.Errorf("--discovery %s: a discovery snapshot is a file", p)
		}
		c.Discovery = append(c.Discovery, filepath.ToSlash(p))
	}
	return c, nil
}

// catalogComment is written into a project document whose catalog is
// empty, so the author sees what arch extract needs before a watched
// resource resolves.
const catalogComment = `  # arch extract resolves watched resources through the catalog. List the
  # CRD manifests under crds and the discovery snapshots (JSON lists of
  # APIResourceList) under discovery, or run arch init with --crd and
  # --discovery.
  catalog:
    crds: []
    discovery: []
`

// writeProject writes the project document. When the catalog is empty it
// adds an empty catalog section with catalogComment, which the YAML
// encoder would omit.
func writeProject(name string, spec project.Spec) error {
	if err := krm.Write(projectFile, project.Kind, name, spec); err != nil {
		return err
	}
	if len(spec.Catalog.CRDs)+len(spec.Catalog.Discovery) > 0 {
		return nil
	}
	b, err := os.ReadFile(projectFile)
	if err != nil {
		return err
	}
	s := string(b)
	i := strings.Index(s, "\nspec:\n")
	if i < 0 {
		return fmt.Errorf("%s: no spec section", projectFile)
	}
	i += len("\nspec:\n")
	return os.WriteFile(projectFile, []byte(s[:i]+catalogComment+s[i:]), 0o644)
}
