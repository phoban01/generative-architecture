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
	"github.com/phoban01/generative-architecture/pkg/catalog"
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
	// processFile holds the IR process of the generated model, so arch check
	// can link the steps of a counterexample to their source.
	processFile = "process.yaml"
	processKind = "Process"
	// counterexamplesDir holds the violating trace arch check kept and its
	// execution view.
	counterexamplesDir = "counterexamples"
)

// reconcileMethod is the method the typed frontend lowers.
const reconcileMethod = "Reconcile"

// generated is what arch generate derives from a project.
type generated struct {
	project     project.Project
	projectPath string // absolute
	doc         ir.Document
	abs         *abstraction.Abstraction
	// controllers holds one entry per declared controller of the IR, in IR
	// order; spec is their composed model, or the one controller's model.
	controllers []controllerOutput
	spec        *model.Spec // nil when the project has no controller
	// links are the resource bindings between the controllers, derived
	// from their watches and effects (ADR-0010); empty for one controller.
	links    []composition.Link
	effects  []ir.Effect
	irSHA256 string
}

// controllerOutput is what generation derives from one controller.
type controllerOutput struct {
	controller ir.Controller
	decl       project.Controller
	spec       model.Spec
	effects    []effect.Descriptor
	profile    cr.GroupProfile
	process    ir.Process
	irEffects  []ir.Effect
}

// name is the controller's name in the runtime model and in the composed
// model.
func (o controllerOutput) name() string { return o.decl.Name }

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
		return gen, missingIR(irPath, filepath.Join(g.out, supportFile))
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
	if len(gen.doc.Controllers) == 0 {
		return gen, nil
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
	for _, c := range gen.doc.Controllers {
		o, err := generateController(g.project, p, c, abs, cat)
		if err != nil {
			return gen, err
		}
		gen.controllers = append(gen.controllers, o)
	}
	spec, links, err := gen.compose(properties)
	if err != nil {
		return gen, err
	}
	gen.spec, gen.links = &spec, links
	// Temporal definitions are validated here as well as in arch check, so
	// a bad constraints document fails before any sampling.
	compiled, err := model.Compile(spec)
	if err != nil {
		return gen, err
	}
	if _, err := loadTemporal(p, compiled); err != nil {
		return gen, err
	}
	for _, o := range gen.controllers {
		gen.effects = append(gen.effects, o.irEffects...)
	}
	return gen, nil
}

// generateController extracts one controller's Reconcile and builds its
// model: its own process, runtime and cache state over the shared
// abstraction state.
func generateController(projectPath string, p project.Project, c ir.Controller, abs abstraction.Abstraction, cat *catalog.Catalog) (controllerOutput, error) {
	decl, ok := p.Spec.Controller(c.Receiver)
	if !ok {
		return controllerOutput{}, fmt.Errorf("controller %s has no declaration; add one with receiver %s under spec.controllers of %s", c.ID, c.Receiver, projectPath)
	}
	l, err := goflow.Load(p.Root, goflow.Selector{Package: c.Package, Receiver: c.Receiver, Method: reconcileMethod})
	if err != nil {
		return controllerOutput{}, fmt.Errorf("controller %s: %w", c.ID, err)
	}
	_, opts, err := extractOptions(l, abs, decl, cat)
	if err != nil {
		return controllerOutput{}, located(p.Root, err)
	}
	program, err := goflow.ExtractTyped(l, opts)
	if err != nil {
		return controllerOutput{}, located(p.Root, err)
	}
	cm := controllerModel{name: modelName(p.Name), abs: abs, decl: decl, initial: p.Spec.Initial, maxTicks: p.Spec.Runtime.MaxTicks, program: *program}
	spec, effects, err := cm.build()
	if err != nil {
		return controllerOutput{}, fmt.Errorf("controller %s: %w", c.ID, err)
	}
	out := controllerOutput{controller: c, decl: decl, spec: spec, effects: effects}
	if out.profile, err = cm.profile(); err != nil {
		return controllerOutput{}, err
	}
	file := relative(p.Root, l.Path)
	if out.process, err = irProcess(c, *program, effects, file); err != nil {
		return controllerOutput{}, err
	}
	out.irEffects = irEffects(c, effects)
	return out, nil
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
// the controllers' effects, and, when a model exists, the context view with
// the derived bindings and one process and one runtime view per
// controller. With one controller the views keep the names process and
// runtime; with several they carry the controller's name.
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
	components, evidence, sources := gen.components()
	if err := add(likec4.ContextView("context", components, evidence, gen.links, sources)); err != nil {
		return w, err
	}
	// The runtime profile is declared in the project document.
	source := ir.Source{File: relative(gen.project.Root, gen.projectPath)}
	for _, o := range gen.controllers {
		process, runtime := "process", "runtime"
		if len(gen.controllers) > 1 {
			process, runtime = process+"_"+o.name(), runtime+"_"+o.name()
		}
		if err := add(likec4.ProcessView(process, "Process: "+o.controller.Name+"."+reconcileMethod, o.process)); err != nil {
			return w, err
		}
		if err := add(likec4.RuntimeView(runtime, o.profile, source)); err != nil {
			return w, err
		}
	}
	return w, w.Validate()
}

// components presents the controllers as composition components with
// their derived resource ports, with the evidence and source of each.
func (gen generated) components() ([]composition.Component, map[string]ir.Evidence, map[string]ir.Source) {
	var components []composition.Component
	evidence := map[string]ir.Evidence{}
	sources := map[string]ir.Source{}
	for _, o := range gen.controllers {
		var watches []ir.Watch
		for _, w := range gen.doc.Watches {
			if w.Controller == o.controller.ID {
				watches = append(watches, w)
			}
		}
		c := composition.Component{Name: o.name(), Model: o.spec, Evidence: ir.Extracted, Watches: watches, Effects: o.irEffects}
		c.Ports = composition.PortsFromIR(c.Name, watches, o.irEffects)
		components = append(components, c)
		evidence[c.Name] = ir.Extracted
		sources[c.Name] = o.controller.Provenance.Source
	}
	return components, evidence, sources
}

// processFileOf names the process document of a controller: process.yaml
// for the only controller, process-<name>.yaml for one of several.
func (gen generated) processFileOf(o controllerOutput) string {
	if len(gen.controllers) == 1 {
		return processFile
	}
	return "process-" + o.name() + ".yaml"
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
		// A process document of an earlier run is stale.
		stale, _ := filepath.Glob(filepath.Join(out, "process*.yaml"))
		for _, f := range stale {
			if err := os.Remove(f); err != nil {
				return err
			}
		}
		for _, o := range gen.controllers {
			if err := krm.WriteResult(filepath.Join(out, gen.processFileOf(o)), processKind, gen.project.Name, o.process); err != nil {
				return err
			}
		}
		if err := os.WriteFile(filepath.Join(out, modelFile), []byte(m.Quint()), 0o644); err != nil {
			return err
		}
	} else {
		for _, f := range []string{specFile, modelFile, processFile} {
			if err := os.Remove(filepath.Join(out, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	w, err := gen.workspace()
	if err != nil {
		return err
	}
	if gen.project.Spec.ViewHints != "" {
		// Presentation hints change titles, groups and layouts only; a
		// hint that names an unknown element or view fails generation.
		hints, err := likec4.LoadHints(filepath.Join(gen.project.Dir, gen.project.Spec.ViewHints))
		if err != nil {
			return err
		}
		if err := likec4.Apply(&w, hints); err != nil {
			return err
		}
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

// missingIR explains a missing IR document. When the support report beside
// it lists unsupported constructs, arch extract ran and failed closed, so
// the error says so and names the first construct. Otherwise extract has
// not run. A construct that spans lines is cut at its first line.
func missingIR(irPath, supportPath string) error {
	support, err := report.Read(supportPath)
	if err != nil || len(support.Unsupported) == 0 {
		return fmt.Errorf("%s not found; run arch extract first", irPath)
	}
	first := support.Unsupported[0]
	construct, _, _ := strings.Cut(first.Construct, "\n")
	return fmt.Errorf("%s not found; arch extract failed closed on %d unsupported constructs, listed in %s; first: %s %s at %s:%d", irPath, len(support.Unsupported), supportPath, first.Kind, construct, first.Location.File, first.Location.Line)
}
