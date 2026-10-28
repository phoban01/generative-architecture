package main

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/composition"
	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"
	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/likec4"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/project"
	"github.com/phoban01/generative-architecture/pkg/receipt"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// Files arch generate writes under the --out directory.
const (
	modelFile    = "Model.qnt"
	specFile     = "model.yaml"
	likec4Dir    = "likec4"
	receiptsFile = "receipts.yaml"
)

// reconcileMethod is the method the typed frontend lowers.
const reconcileMethod = "Reconcile"

// generated is what arch generate derives from a project.
type generated struct {
	project     project.Project
	projectPath string // absolute
	doc         ir.Document
	abs         *abstraction.Abstraction
	spec        *model.Spec // nil when the project has no controller
	process     ir.Process
	profile     cr.GroupProfile
	effects     []ir.Effect
	irSHA256    string
}

// runGenerate reads <out>/ir.yaml, which arch extract writes, and the
// project's Abstraction document. It builds the model of the project's
// controller and writes it as <out>/model.yaml and <out>/Model.qnt, writes
// the LikeC4 workspace under <out>/likec4 and the input receipt as
// <out>/receipts.yaml.
func runGenerate(g globals, args []string) error {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("arch generate: unexpected arguments %v", fs.Args())
	}
	gen, err := generate(g)
	if err != nil {
		return fmt.Errorf("arch generate: %w", err)
	}
	if err := gen.write(g.out); err != nil {
		return fmt.Errorf("arch generate: %w", err)
	}
	if gen.spec == nil {
		fmt.Fprintf(g.stdout, "no controllers in %s: wrote the workspace and receipt, no model\n", filepath.Join(g.out, irFile))
	}
	return nil
}

// generate loads the project and the extracted IR and derives the model,
// its process and its effects.
func generate(g globals) (generated, error) {
	var gen generated
	p, err := project.Load(g.project)
	if err != nil {
		return gen, err
	}
	gen.project = p
	if gen.projectPath, err = filepath.Abs(g.project); err != nil {
		return gen, err
	}
	irPath := filepath.Join(g.out, irFile)
	b, err := os.ReadFile(irPath)
	if errors.Is(err, os.ErrNotExist) {
		return gen, fmt.Errorf("%s not found; run arch extract first", irPath)
	}
	if err != nil {
		return gen, err
	}
	gen.irSHA256 = fmt.Sprintf("%x", sha256.Sum256(b))
	if gen.doc, err = ir.Read(irPath); err != nil {
		return gen, err
	}
	if err := checkSupport(g, p, gen.doc); err != nil {
		return gen, err
	}
	switch n := len(gen.doc.Controllers); {
	case n == 0:
		return gen, nil
	case n > 1:
		return gen, fmt.Errorf("%w: %s has %d controllers; composing more than one controller into one model is not supported yet", errors.ErrUnsupported, irPath, n)
	}
	c := gen.doc.Controllers[0]
	decl, ok := p.Spec.Controller(c.Receiver)
	if !ok {
		return gen, fmt.Errorf("controller %s has no declaration; add one with receiver %s under spec.controllers of %s", c.ID, c.Receiver, g.project)
	}
	abs, err := abstraction.Load(filepath.Join(p.Dir, p.Spec.Abstraction))
	if err != nil {
		return gen, err
	}
	gen.abs = &abs
	properties, err := loadConstraints(p)
	if err != nil {
		return gen, err
	}
	cat, err := p.LoadCatalog()
	if err != nil {
		return gen, err
	}
	l, err := goflow.Load(p.Root, goflow.Selector{Package: c.Package, Receiver: c.Receiver, Method: reconcileMethod})
	if err != nil {
		return gen, fmt.Errorf("controller %s: %w", c.ID, err)
	}
	_, opts, err := extractOptions(l, abs, decl, cat)
	if err != nil {
		return gen, located(p.Root, err)
	}
	program, err := goflow.ExtractTyped(l, opts)
	if err != nil {
		return gen, located(p.Root, err)
	}
	cm := controllerModel{name: modelName(p.Name), abs: abs, decl: decl, initial: p.Spec.Initial, maxTicks: p.Spec.Runtime.MaxTicks, program: *program, properties: properties}
	spec, effects, err := cm.build()
	if err != nil {
		return gen, fmt.Errorf("controller %s: %w", c.ID, err)
	}
	gen.spec = &spec
	if gen.profile, err = cm.profile(); err != nil {
		return gen, err
	}
	file := relative(p.Root, l.Path)
	if gen.process, err = irProcess(c, *program, effects, file); err != nil {
		return gen, err
	}
	gen.effects = irEffects(c, effects)
	return gen, nil
}

// checkSupport applies the mode to the support report arch extract wrote
// beside the IR, with the unresolved facts of doc. Verification mode
// needs the report; inventory mode goes on without it.
func checkSupport(g globals, p project.Project, doc ir.Document) error {
	path := filepath.Join(g.out, supportFile)
	var support report.Support
	if _, err := os.Stat(path); err == nil {
		if support, err = report.Read(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if g.strict {
		return fmt.Errorf("%w: %s not found; run arch extract first", errStrict, path)
	}
	support.Unresolved = unresolvedFacts(doc)
	return enforce(g, p, support)
}

// located makes the file of each frontend diagnostic relative to the
// module root, so the message reads file:line, one line per diagnostic.
func located(root string, err error) error {
	ds, ok := goflow.AsDiagnostics(err)
	if !ok {
		return err
	}
	for _, d := range ds {
		d.File = relative(root, d.File)
	}
	if len(ds) == 1 {
		return ds[0]
	}
	return goflow.Diagnostics(ds)
}

// loadConstraints reads the properties of every constraints document.
// arch check reads the temporal properties (see loadTemporal).
func loadConstraints(p project.Project) ([]model.Property, error) {
	var out []model.Property
	for _, rel := range p.Spec.Constraints {
		var doc model.Constraints
		if _, err := krm.Read(filepath.Join(p.Dir, rel), constraintsKind, &doc); err != nil {
			return nil, err
		}
		out = append(out, doc.Properties...)
	}
	return out, nil
}

// modelName turns a DNS-1123 project name into a model name:
// config-mirror becomes ConfigMirror.
func modelName(name string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '.' }) {
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}

// irProcess converts the extracted program into an IR process. Node n has
// the IR id "<n>", as the generated action node<n>_<outcome> names it.
func irProcess(c ir.Controller, program goflow.Program, effects []effect.Descriptor, file string) (ir.Process, error) {
	id := strconv.Itoa
	out := ir.Process{ID: c.Name, Controller: c.ID, Method: reconcileMethod, Entry: id(program.Entry), Provenance: c.Provenance}
	for _, n := range program.Nodes {
		node := ir.Node{ID: id(n.ID), Kind: n.Kind, Source: ir.Source{File: file, Line: n.Line}, Code: n.Source}
		switch {
		case n.Effect != "":
			node.Kind, node.Effect, node.Next = ir.NodeEffect, n.Effect, id(n.Next)
		case n.Kind == ir.NodeReturn:
			node.Requeue = n.Requeue
		case n.Kind == ir.NodeBranch:
			node.Expression, node.Next, node.Else = string(n.Expression), id(n.Next), id(n.Else)
		case n.Kind == ir.NodeAssign:
			node.Next = id(n.Next)
		default:
			return ir.Process{}, fmt.Errorf("%s:%d: node %d of kind %s has no IR form", file, n.Line, n.ID, n.Kind)
		}
		out.Nodes = append(out.Nodes, node)
	}
	return out, nil
}

// irEffects records the controller's effects in IR form.
func irEffects(c ir.Controller, effects []effect.Descriptor) []ir.Effect {
	var out []ir.Effect
	for _, d := range effects {
		out = append(out, ir.Effect{
			ID: d.ID, Kind: ir.KindAPI, Actor: c.ID,
			Resource:  ir.GVK{Group: d.Resource.Group, Version: d.Resource.Version, Kind: d.Resource.Kind},
			Operation: d.Operation, Consistency: d.Consistency,
			Evidence: ir.Extracted, Provenance: c.Provenance,
		})
	}
	return out
}

// workspace builds the LikeC4 workspace: the resource view of the IR with
// the controller's effects, and, when a model exists, the context, process
// and runtime views.
func (gen generated) workspace() (likec4.Workspace, error) {
	var w likec4.Workspace
	doc := gen.doc
	doc.Effects = append(append([]ir.Effect(nil), doc.Effects...), gen.effects...)
	add := func(e []likec4.Element, r []likec4.Relation, v likec4.View, err error) error {
		if err != nil {
			return err
		}
		return w.Add(e, r, v)
	}
	if err := add(likec4.ResourceView("resources", doc)); err != nil {
		return w, err
	}
	if gen.spec == nil {
		return w, w.Validate()
	}
	c := gen.doc.Controllers[0]
	name := gen.profile.Controllers[0].Name
	components := []composition.Component{{Name: name, Model: *gen.spec}}
	if err := add(likec4.ContextView("context", components, map[string]ir.Evidence{name: ir.Extracted}, nil, map[string]ir.Source{name: c.Provenance.Source})); err != nil {
		return w, err
	}
	if err := add(likec4.ProcessView("process", "Process: "+c.Name+"."+reconcileMethod, gen.process)); err != nil {
		return w, err
	}
	// The runtime profile is declared in the project document.
	source := ir.Source{File: relative(gen.project.Root, gen.projectPath)}
	if err := add(likec4.RuntimeView("runtime", gen.profile, source)); err != nil {
		return w, err
	}
	return w, w.Validate()
}

// write stores the outputs under out. Without a model it removes stale
// model files, so the directory matches the IR.
func (gen generated) write(out string) error {
	if gen.spec != nil {
		m, err := model.Compile(*gen.spec)
		if err != nil {
			return err
		}
		if err := krm.Write(filepath.Join(out, specFile), "TransitionModel", gen.project.Name, *gen.spec); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, modelFile), []byte(m.Quint()), 0o644); err != nil {
			return err
		}
	} else {
		for _, f := range []string{specFile, modelFile} {
			if err := os.Remove(filepath.Join(out, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	w, err := gen.workspace()
	if err != nil {
		return err
	}
	if err := w.Write(filepath.Join(out, likec4Dir)); err != nil {
		return err
	}
	r, err := gen.receipt()
	if err != nil {
		return err
	}
	return r.write(filepath.Join(out, receiptsFile), gen.project.Name)
}

// generationReceipt is the input closure of a generation: the module files
// of the selected packages and every project document, relative to the
// module root, and the generated inputs read from the --out directory,
// relative to it.
type generationReceipt struct {
	receipt.Receipt `yaml:",inline"`
	Generated       []receipt.File `yaml:"generated"`
}

func (r generationReceipt) write(path, name string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return krm.WriteResult(path, receipt.Kind, name, r)
}

// readReceipt loads the receipt arch generate wrote.
func readReceipt(path string) (generationReceipt, error) {
	var r generationReceipt
	if err := krm.ReadResult(path, receipt.Kind, &r); err != nil {
		return r, err
	}
	return r, r.Validate()
}

// receipt collects the closure. The Go version is the only tool generation
// runs; check adds the Quint version it samples with.
func (gen generated) receipt() (generationReceipt, error) {
	p := gen.project
	abs, err := filepath.Abs(p.Dir)
	if err != nil {
		return generationReceipt{}, err
	}
	dir := relative(p.Root, abs)
	var extra []string
	for _, rel := range p.SortedHashes() {
		extra = append(extra, filepath.ToSlash(filepath.Join(dir, rel)))
	}
	r, err := receipt.Collect(p.Root, p.Spec.Packages, extra, map[string]string{"go": runtime.Version()})
	if err != nil {
		return generationReceipt{}, err
	}
	if gen.abs != nil {
		r.Bounds = boundsOf(gen.abs.Spec.Bounds)
		r.Excludes = gen.abs.Spec.Excludes
	}
	return generationReceipt{Receipt: r, Generated: []receipt.File{{Path: irFile, SHA256: gen.irSHA256}}}, nil
}

// boundsOf repeats the abstraction's bounds under their document names.
func boundsOf(b abstraction.Bounds) map[string]int {
	out := map[string]int{}
	if b.MaxObjectsPerResource > 0 {
		out["maxObjectsPerResource"] = b.MaxObjectsPerResource
	}
	if b.MaxInvocations > 0 {
		out["maxInvocations"] = b.MaxInvocations
	}
	return out
}
