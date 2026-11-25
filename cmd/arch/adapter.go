package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/summaries"
)

// adapterDir holds the generated replay adapter under the output
// directory: a main package that arch replay builds in the project's
// module and runs over the saved traces.
const (
	adapterDir  = "adapter"
	adapterMain = "main_generated.go"
)

// adapterTypes maps the type of a reconciler field to how the replay
// adapter fills it.
var adapterTypes = map[string]string{
	"sigs.k8s.io/controller-runtime/pkg/client.Client": adaptergen.ReplayClient,
	"sigs.k8s.io/controller-runtime/pkg/client.Reader": adaptergen.ReplayReader,
	"*k8s.io/apimachinery/pkg/runtime.Scheme":          adaptergen.ReplayScheme,
	objectKeyType: adaptergen.ReplayKey,
}

// replayAdapter builds the replay adapter of a model of one controller:
// the projection, environment, gating, runtime and program files of a main
// package, by file name. When the model or the controller is outside what
// the adapter covers, it returns the reason instead of files.
func (gen generated) replayAdapter() (map[string][]byte, string, error) {
	if len(gen.controllers) != 1 || len(gen.boundaries) != 0 {
		return nil, "a replay adapter covers a model of one controller without boundaries", nil
	}
	o := gen.controllers[0]
	objects := objectsOf(*gen.abs)
	cached, written, read := map[string]bool{}, map[string]bool{}, map[string]bool{}
	type effectNode struct {
		id, operation, consistency string
		object                     abstraction.Object
	}
	var effects []effectNode
	for _, n := range o.program.Nodes {
		if n.Effect == "" {
			continue
		}
		obj, ok := objects[n.Bindings["$exists"]]
		if !ok {
			return nil, "", fmt.Errorf("effect %s addresses no abstracted object", n.Effect)
		}
		switch {
		case n.Kind == ir.OpGet && n.Consistency == ir.ConsistencyCached:
			cached[obj.object.ID], read[obj.object.ID] = true, true
		case n.Kind == ir.OpGet:
			read[obj.object.ID] = true
		default:
			written[obj.object.ID] = true
		}
		effects = append(effects, effectNode{id: n.Effect, operation: n.Kind, consistency: n.Consistency, object: obj.object})
	}
	sort.Slice(effects, func(i, j int) bool { return effects[i].id < effects[j].id })
	var changed, cachedIDs []string
	for _, id := range sortedObjectIDs(*gen.abs) {
		if read[id] && !written[id] {
			changed = append(changed, id)
		}
		if cached[id] {
			cachedIDs = append(cachedIDs, id)
		}
	}
	const pkg = "main"
	files := map[string][]byte{}
	ps, err := adaptergen.FromSpec(gen.abs.Spec, cachedIDs...)
	if err != nil {
		return nil, "projection: " + err.Error(), nil
	}
	if files["projection_generated.go"], err = adaptergen.GenerateReaderProjection(pkg, ps); err != nil {
		return nil, "projection: " + err.Error(), nil
	}
	if files["environment_generated.go"], err = adaptergen.GenerateReaderValueEnvironment(pkg, gen.abs.Spec, changed, cachedIDs); err != nil {
		return nil, "environment: " + err.Error(), nil
	}
	library := summaries.Builtin()
	var events []adaptergen.BoundaryEvent
	targets := map[string]ir.ObjectRef{}
	for _, e := range effects {
		s, err := library.Lookup(e.operation, e.consistency)
		if err != nil {
			return nil, "", fmt.Errorf("effect %s: %w", e.id, err)
		}
		for _, out := range s.Outcomes {
			switch out.Error {
			case "":
				events = append(events, adaptergen.BoundaryEvent{Name: e.id + "_" + out.Name, Effect: e.id})
			case summaries.ErrorTransient:
				events = append(events, adaptergen.BoundaryEvent{Name: e.id + "_" + out.Name, Effect: e.id, Error: true})
			}
		}
		targets[e.id] = ir.ObjectRef{Namespace: e.object.Namespace, Name: e.object.Name}
	}
	if files["gating_generated.go"], err = adaptergen.GenerateBoundaryGating(pkg, o.process, events, targets); err != nil {
		return nil, "gating: " + err.Error(), nil
	}
	g, err := cr.BuildGroupModel(o.profile)
	if err != nil {
		return nil, "", err
	}
	controller, key := o.profile.Controllers[0].Name, o.profile.Controllers[0].Keys[0]
	prefix := controller + "_" + key.ID + "_"
	dropped := map[string]bool{controller + "_next": true, prefix + "order": true}
	binding := adaptergen.RuntimeBinding{
		Events:    map[string]string{"advance": eventAdvance, prefix + "enqueue": eventEnqueue, prefix + "start": eventBegin, prefix + "fire": eventFire, prefix + "success": eventFinish, prefix + "error": eventFinishError, prefix + "requeue": eventFinishRequeue, prefix + "after": eventFinishAfter, prefix + "errorAfter": eventFinishErrorAfter},
		Variables: map[string]string{},
	}
	for name := range g.Spec.State {
		if !dropped[name] {
			binding.Variables[name] = strings.TrimPrefix(name, prefix)
		}
	}
	if files["runtime_generated.go"], err = adaptergen.GenerateRuntime(pkg, g, binding); err != nil {
		return nil, "runtime: " + err.Error(), nil
	}
	main, reason, err := gen.replayMain(o, cached)
	if err != nil || reason != "" {
		return nil, reason, err
	}
	main.Key = ir.ObjectRef{Namespace: key.Namespace, Name: key.Name}
	if files[adapterMain], err = adaptergen.GenerateReplayMain(pkg, main); err != nil {
		return nil, "program: " + err.Error(), nil
	}
	return files, "", nil
}

// replayMain describes the replay program: the scheme from the Go types
// the reconciler holds, the fixtures from the abstraction's objects and
// their initial values, and the reconciler's fields.
func (gen generated) replayMain(o controllerOutput, cached map[string]bool) (adaptergen.ReplayMain, string, error) {
	var r adaptergen.ReplayMain
	r.Reconciler.Import, r.Reconciler.Name = o.controller.Package, o.controller.Receiver
	r.RetryTicks = o.decl.RetryTicks
	for _, res := range gen.abs.Spec.Resources {
		t, ok := o.goTypes[res.GVK]
		if !ok {
			return r, fmt.Sprintf("the reconciler holds no %s, so the adapter cannot register its Go type", res.GVK.Kind), nil
		}
		r.Types = append(r.Types, adaptergen.ReplayType{Import: t.pkg, Name: t.name, Resource: res.GVK})
		for _, obj := range res.Objects {
			exists, err := existsInitial(gen.project.Spec.Initial, obj.ID)
			if err != nil {
				return r, "", err
			}
			if exists == "false" {
				continue
			}
			fixture := adaptergen.ReplayObject{Resource: res.GVK, Namespace: obj.Namespace, Name: obj.Name, Cached: cached[obj.ID]}
			for _, f := range res.Fields {
				literal, ok := initialValue(gen.project.Spec.Initial, res, obj.ID, f)
				if !ok {
					literal = abstraction.Initial(f.Type)
				}
				fixture.Fields = append(fixture.Fields, adaptergen.ReplayValue{Path: abstraction.JSONPath(f.Path), Type: f.Type, Literal: literal})
			}
			r.Objects = append(r.Objects, fixture)
		}
	}
	for _, f := range o.fields {
		kind, ok := adapterTypes[f.Type]
		if !ok {
			return r, fmt.Sprintf("field %s of %s has type %s, which the adapter cannot supply", f.Name, o.controller.Receiver, f.Type), nil
		}
		field := adaptergen.ReplayField{Name: f.Name, Kind: kind}
		if kind == adaptergen.ReplayKey {
			id, declared := o.decl.ObjectKeys[o.controller.Package+"."+o.controller.Receiver+"."+f.Name]
			if !declared {
				return r, fmt.Sprintf("key field %s of %s addresses no declared object; declare it under objectKeys", f.Name, o.controller.Receiver), nil
			}
			obj, found := objectByID(*gen.abs, id)
			if !found {
				return r, "", fmt.Errorf("objectKeys: %s is not an abstracted object", id)
			}
			field.Key = ir.ObjectRef{Namespace: obj.Namespace, Name: obj.Name}
		}
		r.Reconciler.Fields = append(r.Reconciler.Fields, field)
	}
	return r, "", nil
}

// sortedObjectIDs lists the abstraction's object IDs in document order.
func sortedObjectIDs(abs abstraction.Abstraction) []string {
	var out []string
	for _, r := range abs.Spec.Resources {
		for _, o := range r.Objects {
			out = append(out, o.ID)
		}
	}
	return out
}

// objectByID finds an abstraction object.
func objectByID(abs abstraction.Abstraction, id string) (abstraction.Object, bool) {
	for _, r := range abs.Spec.Resources {
		for _, o := range r.Objects {
			if o.ID == id {
				return o, true
			}
		}
	}
	return abstraction.Object{}, false
}

// writeAdapter writes the replay adapter under <out>/adapter, or removes a
// stale one and reports why none was written.
func (gen generated) writeAdapter(out string) ([]string, string, error) {
	dir := filepath.Join(out, adapterDir)
	if err := os.RemoveAll(dir); err != nil {
		return nil, "", err
	}
	if gen.spec == nil {
		return nil, "", nil
	}
	files, reason, err := gen.replayAdapter()
	if err != nil || reason != "" {
		return nil, reason, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var written []string
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), files[name], 0o644); err != nil {
			return nil, "", err
		}
		written = append(written, filepath.ToSlash(filepath.Join(adapterDir, name)))
	}
	return written, "", nil
}
