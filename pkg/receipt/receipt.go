// Package receipt records the input closure of an evidence run: every Go
// file of the selected packages and their in-module transitive imports, the
// module definition, the extra documents consumed (schemas, configuration,
// abstraction and boundary documents) and the exact resolved tool versions
// (EVID-1, EVID-5). Verify re-collects the closure and reports drift.
package receipt

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/phoban01/generative-architecture/pkg/krm"
)

// Kind is the KRM kind of a receipt document.
const Kind = "EvidenceReceipt"

// Drift kinds reported by Verify and Compare.
const (
	Changed = "changed"
	Missing = "missing"
	Added   = "added"
	Tool    = "tool"
)

var (
	hexHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// Exact versions carry no range operators or wildcards. Compatibility
	// ranges live in the tool runners; receipts record what actually ran.
	exactVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]*$`)
)

// File is one consumed file with its hash. Paths are slash-separated and
// relative to the receipt root.
type File struct {
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
}

// Receipt is the recorded input closure of one run.
type Receipt struct {
	// Root is the directory paths are relative to. It is not part of the
	// document: a receipt is read back against a checkout.
	Root     string            `yaml:"-"`
	Module   string            `yaml:"module"`
	Packages []string          `yaml:"packages"`
	Extra    []string          `yaml:"extra,omitempty"`
	Files    []File            `yaml:"files"`
	Tools    map[string]string `yaml:"tools"`
	// Bounds and Excludes qualify the evidence (ADR-0005): the bounds the
	// run explored within and the behaviours it deliberately left out,
	// repeated from the abstraction the caller consumed. Collect leaves them
	// empty and Compare ignores them; the abstraction document recording
	// them is itself an input whose hash is compared.
	Bounds   map[string]int `yaml:"bounds,omitempty"`
	Excludes []string       `yaml:"excludes,omitempty"`
	// Sampling records how sampled traces were drawn. Like Bounds it
	// qualifies the evidence, and Compare ignores it.
	Sampling *Sampling `yaml:"sampling,omitempty"`
}

// Sampling records the bounds and strategy of a sampled run: how many
// traces of at most how many steps, from which seed, and how the next
// action was drawn. A weighted strategy lists the actions it weighted;
// every other action has weight 1. Sampled evidence is bounded, never
// exhaustive.
type Sampling struct {
	Samples  int            `yaml:"samples"`
	Steps    int            `yaml:"steps"`
	Seed     int64          `yaml:"seed"`
	Strategy string         `yaml:"strategy"`
	Weights  map[string]int `yaml:"weights,omitempty"`
}

// Validate requires positive bounds and a known strategy with consistent
// parameters: "uniform" has no weights; "weighted" has named, positive
// weights.
func (s Sampling) Validate() error {
	if s.Samples <= 0 || s.Steps <= 0 {
		return fmt.Errorf("receipt: sampling bounds must be positive, got %d samples of %d steps", s.Samples, s.Steps)
	}
	switch s.Strategy {
	case "uniform":
		if len(s.Weights) > 0 {
			return fmt.Errorf("receipt: uniform sampling takes no weights")
		}
	case "weighted":
		if len(s.Weights) == 0 {
			return fmt.Errorf("receipt: weighted sampling needs weights")
		}
		for name, w := range s.Weights {
			if name == "" || w <= 0 {
				return fmt.Errorf("receipt: sampling weight %q must be named and positive, got %d", name, w)
			}
		}
	default:
		return fmt.Errorf("receipt: unknown sampling strategy %q", s.Strategy)
	}
	return nil
}

// Drift is one difference between a receipt and the current inputs.
type Drift struct {
	Kind string
	Path string
	// Want is the recorded hash or version, Got the current one; either is
	// empty when the entry exists on one side only.
	Want, Got string
}

func (d Drift) String() string {
	switch d.Kind {
	case Changed:
		return fmt.Sprintf("%s changed (%s -> %s)", d.Path, short(d.Want), short(d.Got))
	case Missing:
		return fmt.Sprintf("%s missing", d.Path)
	case Added:
		return fmt.Sprintf("%s added", d.Path)
	}
	return fmt.Sprintf("tool %s: recorded %q, now %q", d.Path, d.Want, d.Got)
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// Collect lists the consumed closure under root: every Go, embedded and
// other build input of the selected package patterns and of their
// transitive imports from the same module, the module's go.mod and go.sum,
// plus the extra paths (files, or directories walked recursively), each with
// its SHA-256, and the exact tool versions.
func Collect(root string, patterns []string, extra []string, tools map[string]string) (Receipt, error) {
	if len(patterns) == 0 {
		return Receipt{}, fmt.Errorf("receipt: at least one package is required")
	}
	if err := checkTools(tools); err != nil {
		return Receipt{}, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Receipt{}, err
	}
	pkgs, err := packages.Load(&packages.Config{Dir: abs, Mode: packages.NeedName | packages.NeedFiles | packages.NeedEmbedFiles | packages.NeedImports | packages.NeedModule}, patterns...)
	if err != nil {
		return Receipt{}, fmt.Errorf("receipt: %w", err)
	}
	if len(pkgs) == 0 {
		return Receipt{}, fmt.Errorf("receipt: %v matched no packages", patterns)
	}
	module := ""
	for _, p := range pkgs {
		if p.Module == nil {
			return Receipt{}, fmt.Errorf("receipt: package %s is not in a module", p.PkgPath)
		}
		if module == "" {
			module = p.Module.Path
		} else if p.Module.Path != module {
			return Receipt{}, fmt.Errorf("receipt: packages span modules %s and %s", module, p.Module.Path)
		}
	}
	paths := map[string]bool{}
	seen := map[string]bool{}
	var visit func(*packages.Package) error
	visit = func(p *packages.Package) error {
		if seen[p.PkgPath] || p.Module == nil || p.Module.Path != module {
			return nil
		}
		seen[p.PkgPath] = true
		if len(p.Errors) > 0 {
			return fmt.Errorf("receipt: load %s: %s", p.PkgPath, p.Errors[0])
		}
		for _, list := range [][]string{p.GoFiles, p.OtherFiles, p.EmbedFiles} {
			for _, f := range list {
				paths[f] = true
			}
		}
		if p.Module.GoMod != "" {
			paths[p.Module.GoMod] = true
			if sum := filepath.Join(filepath.Dir(p.Module.GoMod), "go.sum"); exists(sum) {
				paths[sum] = true
			}
		}
		names := make([]string, 0, len(p.Imports))
		for name := range p.Imports {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := visit(p.Imports[name]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, p := range pkgs {
		if err := visit(p); err != nil {
			return Receipt{}, err
		}
	}
	for _, rel := range extra {
		if err := addExtra(abs, rel, paths); err != nil {
			return Receipt{}, err
		}
	}
	r := Receipt{Root: abs, Module: module, Packages: append([]string(nil), patterns...), Extra: append([]string(nil), extra...), Tools: map[string]string{}}
	for name, version := range tools {
		r.Tools[name] = version
	}
	for path := range paths {
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return Receipt{}, err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return Receipt{}, err
		}
		r.Files = append(r.Files, File{Path: filepath.ToSlash(rel), SHA256: fmt.Sprintf("%x", sha256.Sum256(b))})
	}
	sort.Slice(r.Files, func(i, j int) bool { return r.Files[i].Path < r.Files[j].Path })
	return r, nil
}

func addExtra(root, rel string, paths map[string]bool) error {
	if rel == "" || filepath.IsAbs(rel) {
		return fmt.Errorf("receipt: extra path %q must be relative to the root", rel)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("receipt: %w", err)
	}
	if !info.IsDir() {
		paths[path] = true
		return nil
	}
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			paths[p] = true
		}
		return nil
	})
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func checkTools(tools map[string]string) error {
	for name, version := range tools {
		if name == "" {
			return fmt.Errorf("receipt: tool with empty name")
		}
		if !exactVersion.MatchString(version) {
			return fmt.Errorf("receipt: tool %s needs an exact version, got %q", name, version)
		}
	}
	return nil
}

// Verify re-collects the closure of a receipt from its Root with the same
// packages, extra paths and recorded tool versions and reports changed,
// missing and added files. Tool versions are compared by Compare when the
// caller has resolved the current ones.
func Verify(r Receipt) ([]Drift, error) {
	if r.Root == "" {
		return nil, fmt.Errorf("receipt: root is required to verify")
	}
	var extra []string
	for _, rel := range r.Extra {
		// A removed extra path is drift, not a collection failure.
		if exists(filepath.Join(r.Root, filepath.FromSlash(rel))) {
			extra = append(extra, rel)
		}
	}
	current, err := Collect(r.Root, r.Packages, extra, r.Tools)
	if err != nil {
		return nil, err
	}
	return Compare(r, current), nil
}

// Compare reports the differences between a recorded receipt and a current
// one: changed, missing and added files and tool version differences, in a
// stable order.
func Compare(want, got Receipt) []Drift {
	var out []Drift
	recorded := map[string]string{}
	for _, f := range want.Files {
		recorded[f.Path] = f.SHA256
	}
	current := map[string]string{}
	for _, f := range got.Files {
		current[f.Path] = f.SHA256
	}
	for path, hash := range recorded {
		now, ok := current[path]
		switch {
		case !ok:
			out = append(out, Drift{Kind: Missing, Path: path, Want: hash})
		case now != hash:
			out = append(out, Drift{Kind: Changed, Path: path, Want: hash, Got: now})
		}
	}
	for path, hash := range current {
		if _, ok := recorded[path]; !ok {
			out = append(out, Drift{Kind: Added, Path: path, Got: hash})
		}
	}
	for name, version := range want.Tools {
		if now := got.Tools[name]; now != version {
			out = append(out, Drift{Kind: Tool, Path: name, Want: version, Got: now})
		}
	}
	for name, version := range got.Tools {
		if _, ok := want.Tools[name]; !ok {
			out = append(out, Drift{Kind: Tool, Path: name, Got: version})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Validate checks a receipt's shape: a module, packages, well-formed
// relative paths with hex hashes, and exact tool versions.
func (r Receipt) Validate() error {
	if r.Module == "" || len(r.Packages) == 0 {
		return fmt.Errorf("receipt: module and packages are required")
	}
	if len(r.Files) == 0 {
		return fmt.Errorf("receipt: no files recorded")
	}
	seen := map[string]bool{}
	for _, f := range r.Files {
		if f.Path == "" || filepath.IsAbs(f.Path) || strings.HasPrefix(f.Path, "../") || seen[f.Path] {
			return fmt.Errorf("receipt: invalid or duplicate file path %q", f.Path)
		}
		seen[f.Path] = true
		if !hexHash.MatchString(f.SHA256) {
			return fmt.Errorf("receipt: %s: invalid sha256 %q", f.Path, f.SHA256)
		}
	}
	for name, n := range r.Bounds {
		if name == "" || n <= 0 {
			return fmt.Errorf("receipt: bound %q must be named and positive, got %d", name, n)
		}
	}
	if r.Sampling != nil {
		if err := r.Sampling.Validate(); err != nil {
			return err
		}
	}
	return checkTools(r.Tools)
}

// Write stores a receipt as an EvidenceReceipt status document.
func Write(path, name string, r Receipt) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return krm.WriteResult(path, Kind, name, r)
}

// Read loads an EvidenceReceipt document and binds it to root, the
// checkout its paths are relative to.
func Read(path, root string) (Receipt, error) {
	var r Receipt
	if err := krm.ReadResult(path, Kind, &r); err != nil {
		return r, err
	}
	if err := r.Validate(); err != nil {
		return r, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return r, err
	}
	r.Root = abs
	return r, nil
}
