// Package project loads the Project document: the one authored KRM document
// that names a module's packages, catalog sources, constraints, boundary
// models, runtime profile and bounds (ADOPT-1). Everything else the tools
// consume is derived from it.
package project

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/module"

	"generative-architecture/pkg/catalog"
	"generative-architecture/pkg/krm"
)

// Kind is the KRM kind of a project document.
const Kind = "Project"

var boundName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// Catalog names the sources of the resource catalog: CRD manifests and
// saved API discovery snapshots, relative to the document.
type Catalog struct {
	CRDs      []string `yaml:"crds,omitempty"`
	Discovery []string `yaml:"discovery,omitempty"`
}

// Runtime selects the runtime profile the controllers run under.
type Runtime struct {
	Profile string `yaml:"profile,omitempty"`
	// MaxTicks bounds the model clock of the runtime profile.
	MaxTicks int `yaml:"maxTicks,omitempty"`
}

// Spec is the payload of a Project document. Package patterns are Go package
// patterns relative to the module root (as `go build` reads them); every
// other path is relative to the document.
type Spec struct {
	Module      string         `yaml:"module"`
	Packages    []string       `yaml:"packages"`
	Catalog     Catalog        `yaml:"catalog,omitempty"`
	Constraints []string       `yaml:"constraints,omitempty"`
	Boundaries  []string       `yaml:"boundaries,omitempty"`
	Runtime     Runtime        `yaml:"runtime,omitempty"`
	Bounds      map[string]int `yaml:"bounds,omitempty"`
	// Abstraction names the Abstraction document that maps objects to
	// bounded model state (ADR-0005).
	Abstraction string `yaml:"abstraction,omitempty"`
	// Controllers declares the model facts of each reconciler that the
	// source and the abstraction do not carry.
	Controllers []Controller `yaml:"controllers,omitempty"`
	// Initial is the initial value of each abstracted object's selected
	// field, by object ID, as a model literal.
	Initial map[string]string `yaml:"initial,omitempty"`
}

// Project is a loaded document with its resolved locations and input hashes.
type Project struct {
	Name string
	Spec Spec
	// Dir is the directory of the document; Root is the module root that
	// declares spec.module, found by walking up from Dir.
	Dir, Root string
	// Hashes maps each consumed document (the project document itself,
	// catalog sources, constraints and boundaries), relative to Dir, to its
	// SHA-256.
	Hashes map[string]string
}

// Load reads and validates a Project document, checks that every referenced
// path exists and records the hash of every referenced document.
func Load(path string) (Project, error) {
	var p Project
	meta, err := krm.Read(path, Kind, &p.Spec)
	if err != nil {
		return p, err
	}
	p.Name = meta.Name
	p.Dir = filepath.Dir(path)
	if err = p.Spec.Validate(); err != nil {
		return p, fmt.Errorf("%s: %w", path, err)
	}
	p.Root, err = findModuleRoot(p.Dir, p.Spec.Module)
	if err != nil {
		return p, fmt.Errorf("%s: %w", path, err)
	}
	for _, pattern := range p.Spec.Packages {
		if err = checkPackage(p.Root, pattern); err != nil {
			return p, fmt.Errorf("%s: package %s: %w", path, pattern, err)
		}
	}
	p.Hashes = map[string]string{}
	if err = p.record(filepath.Base(path), path); err != nil {
		return p, err
	}
	for _, rel := range p.Documents() {
		if err = p.record(rel, filepath.Join(p.Dir, rel)); err != nil {
			return p, fmt.Errorf("%s: %w", path, err)
		}
	}
	return p, nil
}

// Validate checks the document payload without touching the file system.
func (s Spec) Validate() error {
	if err := module.CheckImportPath(s.Module); err != nil {
		return fmt.Errorf("invalid module: %w", err)
	}
	if len(s.Packages) == 0 {
		return fmt.Errorf("at least one package is required")
	}
	seen := map[string]bool{}
	for _, pattern := range s.Packages {
		dir := strings.TrimSuffix(pattern, "/...")
		if !strings.HasPrefix(pattern, "./") || strings.HasSuffix(dir, "/") || strings.Contains(dir, "..") {
			return fmt.Errorf("package %q must be a relative pattern such as ./pkg/name or ./pkg/...", pattern)
		}
		if seen[pattern] {
			return fmt.Errorf("package %q listed twice", pattern)
		}
		seen[pattern] = true
	}
	for field, paths := range map[string][]string{"catalog.crds": s.Catalog.CRDs, "catalog.discovery": s.Catalog.Discovery, "constraints": s.Constraints, "boundaries": s.Boundaries} {
		for _, rel := range paths {
			if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(filepath.ToSlash(rel), "../") {
				return fmt.Errorf("%s: path %q must be relative to the document and inside its directory", field, rel)
			}
		}
	}
	for name, v := range s.Bounds {
		if !boundName.MatchString(name) {
			return fmt.Errorf("bound %q must be an identifier", name)
		}
		if v <= 0 {
			return fmt.Errorf("bound %s must be positive, got %d", name, v)
		}
	}
	return s.validateModel()
}

// Documents lists every referenced document path (relative to Dir) in a
// stable order: catalog CRDs, discovery snapshots, constraints, boundaries
// and the abstraction.
func (p Project) Documents() []string {
	var out []string
	out = append(out, p.Spec.Catalog.CRDs...)
	out = append(out, p.Spec.Catalog.Discovery...)
	out = append(out, p.Spec.Constraints...)
	out = append(out, p.Spec.Boundaries...)
	if p.Spec.Abstraction != "" {
		out = append(out, p.Spec.Abstraction)
	}
	return out
}

// Paths resolves the referenced documents to file paths, relative to the
// process working directory, in the order of Documents.
func (p Project) Paths() []string {
	docs := p.Documents()
	out := make([]string, len(docs))
	for i, rel := range docs {
		out[i] = filepath.Join(p.Dir, rel)
	}
	return out
}

// SortedHashes lists the recorded hashes by path.
func (p Project) SortedHashes() []string {
	names := make([]string, 0, len(p.Hashes))
	for n := range p.Hashes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// LoadCatalog builds the resource catalog from the declared CRD manifests and
// discovery snapshots. Conflicts between sources are errors.
func (p Project) LoadCatalog() (*catalog.Catalog, error) {
	c := catalog.New()
	var crds []string
	for _, rel := range p.Spec.Catalog.CRDs {
		crds = append(crds, filepath.Join(p.Dir, rel))
	}
	entries, err := catalog.LoadCRDs(crds...)
	if err != nil {
		return nil, err
	}
	for _, rel := range p.Spec.Catalog.Discovery {
		more, err := catalog.LoadDiscovery(filepath.Join(p.Dir, rel))
		if err != nil {
			return nil, err
		}
		entries = append(entries, more...)
	}
	for _, e := range entries {
		if err := c.Add(e); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (p *Project) record(rel, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	p.Hashes[filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(b))
	return nil
}

// checkPackage requires the directory of a package pattern to exist under
// the module root; a trailing /... selects the directory and its subtree.
func checkPackage(root, pattern string) error {
	dir := strings.TrimSuffix(pattern, "/...")
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}

// findModuleRoot walks up from dir to the go.mod that declares module.
func findModuleRoot(dir, mod string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for d := abs; ; d = filepath.Dir(d) {
		if declared, ok := moduleOf(filepath.Join(d, "go.mod")); ok {
			if declared == mod {
				return d, nil
			}
			return "", fmt.Errorf("module %s not found: nearest go.mod at %s declares %s", mod, d, declared)
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("module %s not found: no go.mod above %s", mod, abs)
		}
	}
}

func moduleOf(gomod string) (string, bool) {
	f, err := os.Open(gomod)
	if err != nil {
		return "", false
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), true
		}
	}
	return "", true
}
