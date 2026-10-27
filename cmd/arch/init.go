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
	var pkgs stringList
	fs.Var(&pkgs, "package", "Go package pattern relative to the module root, such as ./... (repeatable; default ./...)")
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
	spec := project.Spec{
		Module:      *module,
		Packages:    pkgs,
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
	if err := krm.Write(projectFile, project.Kind, *name, spec); err != nil {
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
