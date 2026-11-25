package adaptergen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

// ReplayType registers the Go type of one resource in the replay's scheme.
type ReplayType struct {
	Import   string
	Name     string
	Resource ir.GVK
}

// ReplayObject is a fixture object that exists before a replay starts, with
// the initial value of each selected field. Cached marks an object the
// controller reads through its informer cache: the replay keeps a copy in a
// separate cache that only environment deliveries update.
type ReplayObject struct {
	Resource  ir.GVK
	Namespace string
	Name      string
	Fields    []ReplayValue
	Cached    bool
}

// ReplayValue is the initial value of one field: a Go literal of Type
// (string, bool or int), rendered as the Go expression Literal.
type ReplayValue struct {
	Path    []string
	Type    string
	Literal string
}

// Field kinds of a replay reconciler: how the replay fills each struct
// field of the reconciler.
const (
	// ReplayClient is a client.Client: the manager's client, reading the
	// cache and writing the API.
	ReplayClient = "client"
	// ReplayReader is a client.Reader that reads the API.
	ReplayReader = "reader"
	// ReplayKey is a client.ObjectKey that addresses an abstraction object.
	ReplayKey = "key"
	// ReplayScheme is the *runtime.Scheme of the replay.
	ReplayScheme = "scheme"
)

// ReplayField is one struct field of the reconciler and how the replay
// fills it. Key is set for a ReplayKey field.
type ReplayField struct {
	Name string
	Kind string
	Key  ir.ObjectRef
}

// ReplayMain describes the replay program of one controller: the scheme,
// the fixtures, the reconciler and the worker that drives it. The
// generated program takes the generated model, replays each trace it is
// given through the implementation and prints one JSON result per trace.
type ReplayMain struct {
	Types      []ReplayType
	Objects    []ReplayObject
	Reconciler struct {
		Import string
		Name   string
		Fields []ReplayField
	}
	// Actor is the effect actor the observer reports; empty for a model
	// of one controller.
	Actor string
	// Key is the reconcile request the worker serves.
	Key ir.ObjectRef
	// RetryTicks is the worker's retry delay.
	RetryTicks int
}

type replayImport struct{ Alias, Path string }

type replayObject struct {
	Resource        ir.GVK
	Namespace, Name string
	Fields          []ReplayValue
	Cached          bool
	Variable        string
}

type replayField struct {
	Name, Value string
}

type replayData struct {
	Imports    []replayImport
	Types      []struct{ GV, Expr string }
	Objects    []replayObject
	Cache      bool
	Reconciler string
	Fields     []replayField
	Actor      string
	Key        ir.ObjectRef
	RetryTicks int
}

// GenerateReplayMain emits a main package that replays traces through the
// controller r describes, with the projection, environment, gating and
// runtime files of the same package (GenerateReaderProjection,
// GenerateReaderValueEnvironment, GenerateBoundaryGating and
// GenerateRuntime with pkg "main"). The program takes -model, the
// generated TransitionModel, and the ITF traces to replay as arguments. For
// each trace it prints a JSON line: the trace name, the steps replayed, the
// event coverage and, when the replay failed, the failure's category, step,
// event and message. It builds its fixtures from the scheme and objects,
// wires the reconciler as a controller-runtime manager would, and never
// consults expected model state beyond the model it checks against.
func GenerateReplayMain(pkg string, r ReplayMain) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	if r.Reconciler.Import == "" || r.Reconciler.Name == "" {
		return nil, fmt.Errorf("replay: the reconciler needs an import path and a type name")
	}
	if r.Key.Name == "" {
		return nil, fmt.Errorf("replay: the reconcile key needs a name")
	}
	if len(r.Types) == 0 {
		return nil, fmt.Errorf("replay: at least one resource type is required")
	}
	aliases := map[string]string{}
	var imports []replayImport
	alias := func(path string) string {
		if a, ok := aliases[path]; ok {
			return a
		}
		a := fmt.Sprintf("p%d", len(aliases))
		aliases[path] = a
		imports = append(imports, replayImport{Alias: a, Path: path})
		return a
	}
	d := replayData{Reconciler: alias(r.Reconciler.Import) + "." + r.Reconciler.Name, Actor: r.Actor, Key: r.Key, RetryTicks: r.RetryTicks}
	seen := map[ir.GVK]bool{}
	for _, t := range r.Types {
		if t.Import == "" || t.Name == "" || t.Resource.Version == "" || t.Resource.Kind == "" {
			return nil, fmt.Errorf("replay: type %+v needs an import path, a name, a version and a kind", t)
		}
		if seen[t.Resource] {
			return nil, fmt.Errorf("replay: resource %s is registered twice", t.Resource)
		}
		seen[t.Resource] = true
		d.Types = append(d.Types, struct{ GV, Expr string }{
			GV:   fmt.Sprintf("schema.GroupVersion{Group: %s, Version: %s}", strconv.Quote(t.Resource.Group), strconv.Quote(t.Resource.Version)),
			Expr: "&" + alias(t.Import) + "." + t.Name + "{}",
		})
	}
	for i, o := range r.Objects {
		if !seen[o.Resource] {
			return nil, fmt.Errorf("replay: object %s/%s is of %s, which no type registers", o.Namespace, o.Name, o.Resource)
		}
		if o.Name == "" {
			return nil, fmt.Errorf("replay: object %d of %s has no name", i, o.Resource)
		}
		fields := make([]ReplayValue, 0, len(o.Fields))
		for _, f := range o.Fields {
			if len(f.Path) == 0 || f.Literal == "" {
				return nil, fmt.Errorf("replay: object %s/%s: a field needs a path and a literal", o.Namespace, o.Name)
			}
			switch f.Type {
			case "string", "bool", "int":
			default:
				return nil, fmt.Errorf("replay: object %s/%s: field %s has type %q; string, bool or int expected", o.Namespace, o.Name, strings.Join(f.Path, "."), f.Type)
			}
			fields = append(fields, f)
		}
		d.Cache = d.Cache || o.Cached
		d.Objects = append(d.Objects, replayObject{Resource: o.Resource, Namespace: o.Namespace, Name: o.Name, Fields: fields, Cached: o.Cached, Variable: fmt.Sprintf("object%d", i)})
	}
	for _, f := range r.Reconciler.Fields {
		value := ""
		switch f.Kind {
		case ReplayClient:
			if d.Cache {
				value = `observer.Client(cr.SplitClient(api, cache), "cached")`
			} else {
				value = `observer.Client(api, "direct")`
			}
		case ReplayReader:
			value = `observer.Reader(api, "direct")`
		case ReplayKey:
			if f.Key.Name == "" {
				return nil, fmt.Errorf("replay: key field %s names no object", f.Name)
			}
			value = fmt.Sprintf("client.ObjectKey{Namespace: %s, Name: %s}", strconv.Quote(f.Key.Namespace), strconv.Quote(f.Key.Name))
		case ReplayScheme:
			value = "scheme"
		default:
			return nil, fmt.Errorf("replay: field %s has kind %q; client, reader, key or scheme expected", f.Name, f.Kind)
		}
		d.Fields = append(d.Fields, replayField{Name: f.Name, Value: value})
	}
	sort.Slice(d.Fields, func(i, j int) bool { return d.Fields[i].Name < d.Fields[j].Name })
	d.Imports = imports
	return render("replay", replayTemplate, struct {
		Package string
		replayData
	}{pkg, d})
}

const replayTemplate = Header + `

package {{.Package}}

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/phoban01/generative-architecture/pkg/conformance"
	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"
	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
{{range .Imports}}	{{.Alias}} {{q .Path}}
{{end}})

// replayResult is what the program prints for one trace.
type replayResult struct {
	Trace    string         ` + "`" + `json:"trace"` + "`" + `
	Steps    int            ` + "`" + `json:"steps"` + "`" + `
	Coverage map[string]int ` + "`" + `json:"coverage,omitempty"` + "`" + `
	Category string         ` + "`" + `json:"category,omitempty"` + "`" + `
	Step     int            ` + "`" + `json:"step,omitempty"` + "`" + `
	Event    string         ` + "`" + `json:"event,omitempty"` + "`" + `
	Message  string         ` + "`" + `json:"message,omitempty"` + "`" + `
}

func main() {
	modelPath := flag.String("model", "", "the generated TransitionModel to replay against")
	timeout := flag.Duration("timeout", 30*time.Second, "time limit per trace")
	flag.Parse()
	if *modelPath == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: replay -model model.yaml trace.itf.json...")
		os.Exit(2)
	}
	var spec model.Spec
	if _, err := krm.Read(*modelPath, "TransitionModel", &spec); err != nil {
		fail(err)
	}
	m, err := model.Compile(spec)
	if err != nil {
		fail(err)
	}
	out := json.NewEncoder(os.Stdout)
	for _, path := range flag.Args() {
		r := replayResult{Trace: filepath.Base(path)}
		trace, err := conformance.ReadITF(path)
		if err != nil {
			r.Category, r.Message = string(conformance.CategoryTool), err.Error()
		} else {
			report, err := replayTrace(m, trace, *timeout)
			r.Steps, r.Coverage = report.Steps, report.Coverage
			if f, ok := conformance.AsFailure(err); ok {
				r.Category, r.Step, r.Event, r.Message = string(f.Category), f.Step, f.Event, f.Error()
			} else if err != nil {
				r.Category, r.Message = string(conformance.CategoryTool), err.Error()
			}
		}
		if err := out.Encode(r); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// replayTrace replays one trace through a fresh implementation.
func replayTrace(m *model.Compiled, trace conformance.Trace, timeout time.Duration) (conformance.Report, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	a, err := newAdapter(ctx, m)
	if err != nil {
		return conformance.Report{}, err
	}
	report, err := conformance.ReplayObserved(ctx, m, trace, a, a.observed)
	closed := a.Close()
	if err == nil {
		err = closed
	}
	return report, err
}

// adapter supplies the fixtures and routes replay events to the generated
// environment, gating and runtime. It receives events, never expected
// state.
type adapter struct {
	api      client.Client
{{if .Cache}}	cache    client.Client
{{end}}	worker   *cr.Worker
	gate     *effect.Gate
	cancel   context.CancelFunc
	observed []string
}

func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
{{range .Types}}	{
		gv := {{.GV}}
		scheme.AddKnownTypes(gv, {{.Expr}})
		metav1.AddToGroupVersion(scheme, gv)
	}
{{end}}	return scheme, nil
}

// fixtures builds the objects that exist before the replay, with the
// initial value of each selected field.
func fixtures() ([]client.Object, []client.Object, error) {
	var api, cache []client.Object
{{range .Objects}}	{
		obj := &unstructured.Unstructured{Object: map[string]any{}}
		obj.SetGroupVersionKind({{gvk .Resource}})
		obj.SetNamespace({{q .Namespace}})
		obj.SetName({{q .Name}})
{{range .Fields}}		if err := unstructured.SetNestedField(obj.Object, {{.Literal}}, {{path .Path}}); err != nil {
			return nil, nil, err
		}
{{end}}		api = append(api, obj)
{{if .Cached}}		cache = append(cache, obj.DeepCopy())
{{end}}	}
{{end}}	return api, cache, nil
}

func newAdapter(ctx context.Context, m *model.Compiled) (*adapter, error) {
	ctx, cancel := context.WithCancel(ctx)
	scheme, err := newScheme()
	if err != nil {
		cancel()
		return nil, err
	}
	objects, cached, err := fixtures()
	if err != nil {
		cancel()
		return nil, err
	}
	_ = cached
	a := &adapter{api: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(), gate: effect.NewGate(), cancel: cancel}
{{if .Cache}}	a.cache = fake.NewClientBuilder().WithScheme(scheme).WithObjects(cached...).Build()
	cache := a.cache
{{end}}	api := a.api
	observer := cr.Observer{Actor: {{q .Actor}}, Registry: m.Effects(), Scheme: scheme, Hook: a.gate.Hook}
	_ = observer
	r := &{{.Reconciler}}{
{{range .Fields}}		{{.Name}}: {{.Value}},
{{end}}	}
	if a.worker, err = cr.NewWorker(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: {{q .Key.Namespace}}, Name: {{q .Key.Name}}}}, {{.RetryTicks}}, r); err != nil {
		cancel()
		return nil, err
	}
	initial, err := m.Initial()
	if err == nil {
		var state map[string]any
		if state, err = a.project(ctx); err == nil {
			for name := range state {
				if _, declared := initial[name]; declared {
					a.observed = append(a.observed, name)
				}
			}
			sort.Strings(a.observed)
		}
	}
	if err != nil {
		_ = a.Close()
		return nil, err
	}
	return a, nil
}

func (a *adapter) Close() error { a.cancel(); return a.worker.Close() }

// Apply routes event to the generated environment, boundary gating or
// runtime routing. The controller takes its own steps between effect
// boundaries, so process events need no driving.
func (a *adapter) Apply(ctx context.Context, event string) error {
	if ok, err := ApplyEnvironment(ctx, a.api{{if .Cache}}, a.cache{{end}}, event); ok {
		return err
	}
	if _, ok, err := ApplyBoundary(ctx, a.gate, event); ok {
		return err
	}
	if ok, err := ApplyRuntime(a.worker, event); ok {
		return err
	}
	if _, ok := conformance.NodeID(event); ok {
		return nil
	}
	return fmt.Errorf("unknown event %s", event)
}

// Project returns the observed model variables.
func (a *adapter) Project(ctx context.Context) (map[string]any, error) {
	state, err := a.project(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(a.observed))
	for _, name := range a.observed {
		out[name] = state[name]
	}
	return out, nil
}

func (a *adapter) project(ctx context.Context) (map[string]any, error) {
	state, err := Project(ctx, a.api{{if .Cache}}, a.cache{{end}})
	if err != nil {
		return nil, err
	}
	for name, v := range ProjectRuntime(a.worker) {
		if _, dup := state[name]; dup {
			return nil, fmt.Errorf("runtime variable %s collides with a projected API variable", name)
		}
		state[name] = v
	}
	return state, nil
}
`
