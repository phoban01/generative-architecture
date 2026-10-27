package adaptergen

import (
	"fmt"
	"go/token"
	"go/types"
	"sort"
	"strconv"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

// Packages every generated proxy imports, by the name it uses for them.
var proxyImports = map[string]string{
	"context": "context",
	"fmt":     "fmt",
	"effect":  "github.com/phoban01/generative-architecture/pkg/effect",
}

type proxyImport struct{ Alias, Path string }

type proxyValue struct {
	Name string // local identifier in the generated method
	Type string
	Key  string // key in Call.Submitted or Result.Snapshot
}

type proxyMethod struct {
	Name     string
	Effect   string
	Params   []proxyValue
	Variadic bool
	Results  []proxyValue
	// Ctx is the context the gate pauses under: the method's own
	// context.Context parameter, or the proxy's Context field.
	Ctx string
	// Err is the local of the method's trailing error result, if any.
	Err string
	// Snapshot are the results other than Err.
	Snapshot []proxyValue
	// Submitted are the parameters other than contexts.
	Submitted []proxyValue
}

type proxyDescriptor struct {
	ID, Actor, Group, Version, Kind, Operation, Consistency string
}

type proxyOutcome struct {
	Name  string
	Error bool
}

type proxyOutcomes struct {
	Effect   string
	Outcomes []proxyOutcome
}

// proxyQualifier renders package qualifiers for generated code in package
// pkg and records the imports they need. The interface's own package is
// not qualified when the file is generated into it.
type proxyQualifier struct {
	self    string
	byPath  map[string]string
	aliases map[string]bool
}

func (q *proxyQualifier) qualify(p *types.Package) string {
	if p.Path() == q.self {
		return ""
	}
	if alias, ok := q.byPath[p.Path()]; ok {
		return alias
	}
	alias := p.Name()
	for i := 1; q.aliases[alias] && proxyImports[alias] != p.Path(); i++ {
		alias = p.Name() + strconv.Itoa(i)
	}
	q.byPath[p.Path()] = alias
	q.aliases[alias] = true
	return alias
}

// GenerateInterfaceProxy emits a file of package pkg defining a proxy for
// the Go interface iface, whose methods named in effects are effects
// (ARCH-13):
//
//	type <Iface>Proxy struct {
//		<Iface>             // the implementation
//		Gate    *effect.Gate
//		Context context.Context
//	}
//	func Release<Iface>(ctx context.Context, gate *effect.Gate, effectID, outcome string) (effect.Call, effect.Result, error)
//
// A method named in effects calls Gate.Hook with the effect's descriptor
// and the method arguments other than contexts as Submitted, keyed by
// parameter name (arg<i> when unnamed). It pauses there until the
// harness releases the call, then
// delegates to the implementation and records its results other than a
// trailing error as the Snapshot, keyed result<i> by position. The pause
// uses the method's context.Context parameter, or Context when it takes
// none. An injected error becomes the trailing error result, with the
// other results zero; a method without one drops it. Other methods
// delegate directly.
//
// Release<Iface> releases the next paused call with a declared outcome of
// an external effect. It refuses an undeclared outcome before it takes a
// call, so the call stays paused. An error outcome injects
// <Iface>OutcomeError without calling the implementation; any other outcome
// calls it and requires it to succeed. API effects declare no outcomes and
// are released by the boundary gating.
//
// The file is generated into the interface's own package when pkg is that
// package's name, and imports it otherwise. The proxy reads no expected
// model state.
func GenerateInterfaceProxy(pkg string, iface *types.Named, effects map[string]ir.Effect) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	if iface == nil || iface.Obj().Pkg() == nil {
		return nil, fmt.Errorf("interface proxy needs a named interface of a package")
	}
	it, ok := iface.Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("%s is not an interface", iface.Obj().Name())
	}
	if iface.TypeParams().Len() > 0 {
		return nil, fmt.Errorf("interface %s is generic; generic interfaces are not supported", iface.Obj().Name())
	}
	name := iface.Obj().Name()
	ipkg := iface.Obj().Pkg()
	q := &proxyQualifier{byPath: map[string]string{}, aliases: map[string]bool{}}
	for alias, path := range proxyImports {
		q.byPath[path], q.aliases[alias] = alias, true
	}
	if pkg == ipkg.Name() {
		q.self = ipkg.Path()
	} else if !token.IsExported(name) {
		return nil, fmt.Errorf("interface %s is not exported from %s", name, ipkg.Path())
	}
	if len(effects) == 0 {
		return nil, fmt.Errorf("interface %s: at least one effect is required", name)
	}
	embedded := types.TypeString(iface, q.qualify)
	methods := map[string]*types.Func{}
	for i := 0; i < it.NumMethods(); i++ {
		methods[it.Method(i).Name()] = it.Method(i)
	}
	names := make([]string, 0, len(effects))
	for m := range effects {
		names = append(names, m)
	}
	sort.Strings(names)
	ids := map[string]bool{}
	var gated []proxyMethod
	var descriptors []proxyDescriptor
	var outcomes []proxyOutcomes
	for _, m := range names {
		e := effects[m]
		fn, ok := methods[m]
		if !ok {
			return nil, fmt.Errorf("interface %s has no method %s", name, m)
		}
		if !fn.Exported() && q.self == "" {
			return nil, fmt.Errorf("interface %s: method %s is not exported", name, m)
		}
		if e.ID == "" || ids[e.ID] {
			return nil, fmt.Errorf("interface %s: method %s needs a distinct effect id, got %q", name, m, e.ID)
		}
		ids[e.ID] = true
		d := proxyDescriptor{ID: e.ID, Actor: e.Actor, Group: e.Resource.Group, Version: e.Resource.Version, Kind: e.Resource.Kind, Operation: e.Operation, Consistency: e.Consistency}
		if e.External() {
			if err := e.Validate(); err != nil {
				return nil, fmt.Errorf("interface %s, method %s: %w", name, m, err)
			}
			d.Operation = ir.KindExternal
			o := proxyOutcomes{Effect: e.ID}
			for _, out := range e.Outcomes {
				o.Outcomes = append(o.Outcomes, proxyOutcome{Name: out.Name, Error: out.Error})
			}
			outcomes = append(outcomes, o)
		} else if e.Kind != "" && e.Kind != ir.KindAPI {
			return nil, fmt.Errorf("interface %s, method %s: unknown effect kind %q", name, m, e.Kind)
		}
		descriptors = append(descriptors, d)
		gated = append(gated, proxyMethodOf(fn, e.ID, q))
	}
	var imports []proxyImport
	for path, alias := range q.byPath {
		if proxyImports[alias] != path {
			imports = append(imports, proxyImport{Alias: alias, Path: path})
		}
	}
	sort.Slice(imports, func(i, j int) bool { return imports[i].Path < imports[j].Path })
	field := name
	return render("interface proxy", proxyTemplate, struct {
		Header, Package, Name, Embedded, Field, Table string
		Imports                                       []proxyImport
		Methods                                       []proxyMethod
		Descriptors                                   []proxyDescriptor
		Outcomes                                      []proxyOutcomes
	}{Header, pkg, name, embedded, field, "proxy" + name, imports, gated, descriptors, outcomes})
}

// proxyMethodOf renders the signature of one gated method with generated
// local names, so parameter names cannot shadow the proxy's own.
func proxyMethodOf(fn *types.Func, effectID string, q *proxyQualifier) proxyMethod {
	sig := fn.Type().(*types.Signature)
	m := proxyMethod{Name: fn.Name(), Effect: effectID, Variadic: sig.Variadic(), Ctx: "p.Context"}
	for i := 0; i < sig.Params().Len(); i++ {
		v := sig.Params().At(i)
		t := v.Type()
		typ := types.TypeString(t, q.qualify)
		if m.Variadic && i == sig.Params().Len()-1 {
			typ = "..." + types.TypeString(t.(*types.Slice).Elem(), q.qualify)
		}
		key := v.Name()
		if key == "" || key == "_" {
			key = fmt.Sprintf("arg%d", i)
		}
		p := proxyValue{Name: fmt.Sprintf("a%d", i), Type: typ, Key: key}
		m.Params = append(m.Params, p)
		if t.String() == "context.Context" {
			if m.Ctx == "p.Context" {
				m.Ctx = p.Name
			}
			continue
		}
		m.Submitted = append(m.Submitted, p)
	}
	for i := 0; i < sig.Results().Len(); i++ {
		t := sig.Results().At(i).Type()
		r := proxyValue{Name: fmt.Sprintf("r%d", i), Type: types.TypeString(t, q.qualify), Key: fmt.Sprintf("result%d", i)}
		m.Results = append(m.Results, r)
		if i == sig.Results().Len()-1 && types.Identical(t, types.Universe.Lookup("error").Type()) {
			m.Err = r.Name
		} else {
			m.Snapshot = append(m.Snapshot, r)
		}
	}
	return m
}

// Signature and call fragments of a proxy method.
func (m proxyMethod) ParamList() string {
	parts := make([]string, len(m.Params))
	for i, p := range m.Params {
		parts[i] = p.Name + " " + p.Type
	}
	return strings.Join(parts, ", ")
}

func (m proxyMethod) ResultList() string {
	parts := make([]string, len(m.Results))
	for i, r := range m.Results {
		parts[i] = r.Type
	}
	if len(parts) > 1 {
		return "(" + strings.Join(parts, ", ") + ")"
	}
	return strings.Join(parts, "")
}

func (m proxyMethod) Args() string {
	parts := make([]string, len(m.Params))
	for i, p := range m.Params {
		parts[i] = p.Name
	}
	s := strings.Join(parts, ", ")
	if m.Variadic {
		s += "..."
	}
	return s
}

func (m proxyMethod) ResultNames() string {
	parts := make([]string, len(m.Results))
	for i, r := range m.Results {
		parts[i] = r.Name
	}
	return strings.Join(parts, ", ")
}

const proxyTemplate = `{{.Header}}

package {{.Package}}

import (
	"context"
	"fmt"

	"github.com/phoban01/generative-architecture/pkg/effect"
{{- range .Imports}}
	{{.Alias}} {{q .Path}}
{{- end}}
)

// {{.Name}}Proxy gates the effects of {{.Embedded}}: a call of a gated
// method pauses at Gate until the harness releases it, then delegates to
// the embedded implementation. Other methods delegate directly.
type {{.Name}}Proxy struct {
	{{.Embedded}}
	Gate *effect.Gate
	// Context bounds the pause of a gated method that takes no context.
	Context context.Context
}

// {{.Table}}Effects describes the effect each gated method performs.
var {{.Table}}Effects = map[string]effect.Descriptor{
{{- range .Descriptors}}
	{{q .ID}}: {Actor: {{q .Actor}}, ID: {{q .ID}}, Resource: effect.Resource{Group: {{q .Group}}, Version: {{q .Version}}, Kind: {{q .Kind}}}, Operation: {{q .Operation}}, Consistency: {{q .Consistency}}},
{{- end}}
}

// {{.Table}}Outcomes lists the declared outcomes of each external effect
// and whether the caller sees each as a failure.
var {{.Table}}Outcomes = map[string]map[string]bool{
{{- range .Outcomes}}
	{{q .Effect}}: { {{- range $i, $o := .Outcomes}}{{if $i}}, {{end}}{{q $o.Name}}: {{$o.Error}}{{end -}} },
{{- end}}
}
{{range .Methods}}
// {{.Name}} performs effect {{.Effect}} at the gate.
func (p {{$.Name}}Proxy) {{.Name}}({{.ParamList}}) {{.ResultList}} {
{{- range .Results}}
	var {{.Name}} {{.Type}}
{{- end}}
	{{if .Err}}result := {{end}}p.Gate.Hook({{.Ctx}}, effect.Call{Descriptor: {{$.Table}}Effects[{{q .Effect}}], Submitted: map[string]any{ {{- range $i, $p := .Submitted}}{{if $i}}, {{end}}{{q $p.Key}}: {{$p.Name}}{{end -}} }}, func() effect.Result {
		{{if .Results}}{{.ResultNames}} = {{end}}p.{{$.Field}}.{{.Name}}({{.Args}})
		return effect.Result{Snapshot: map[string]any{ {{- range $i, $r := .Snapshot}}{{if $i}}, {{end}}{{q $r.Key}}: {{$r.Name}}{{end -}} }{{if .Err}}, Err: {{.Err}}{{end}}}
	})
{{- if .Err}}
	{{.Err}} = result.Err
{{- end}}
{{- if .Results}}
	return {{.ResultNames}}
{{- end}}
}
{{end}}
// {{.Name}}OutcomeError is the error a gated call returns when the harness
// releases it with a declared error outcome. The implementation is not
// called.
type {{.Name}}OutcomeError struct{ Effect, Outcome string }

func (e *{{.Name}}OutcomeError) Error() string {
	return fmt.Sprintf("effect %s: outcome %s", e.Effect, e.Outcome)
}

// Release{{.Name}} releases the next paused call of {{.Name}}Proxy with the
// declared outcome of effect effectID. It refuses an undeclared outcome
// before it takes a call, so the call stays paused. It waits for the call
// and requires it to perform effectID; a mismatched call is left paused for
// the caller to release by cancelling its context. An error outcome injects
// {{.Name}}OutcomeError without calling the implementation; any other
// outcome calls it and requires it to succeed. It returns the call, with
// its submitted arguments, and the result.
func Release{{.Name}}(ctx context.Context, gate *effect.Gate, effectID, outcome string) (effect.Call, effect.Result, error) {
	outcomes, ok := {{.Table}}Outcomes[effectID]
	if !ok {
		return effect.Call{}, effect.Result{}, fmt.Errorf("effect %s declares no outcomes", effectID)
	}
	failing, ok := outcomes[outcome]
	if !ok {
		return effect.Call{}, effect.Result{}, fmt.Errorf("effect %s: outcome %q is not declared", effectID, outcome)
	}
	b, err := gate.Next(ctx)
	if err != nil {
		return effect.Call{}, effect.Result{}, err
	}
	if b.Call.Descriptor.ID != effectID {
		return b.Call, effect.Result{}, fmt.Errorf("expected effect %s, got %s", effectID, b.Call.Descriptor.ID)
	}
	var injected error
	if failing {
		injected = &{{.Name}}OutcomeError{Effect: effectID, Outcome: outcome}
	}
	result, err := b.Complete(ctx, injected)
	if err != nil {
		return b.Call, result, err
	}
	if !failing && result.Err != nil {
		return b.Call, result, fmt.Errorf("effect %s: outcome %s succeeds, but the call failed: %w", effectID, outcome, result.Err)
	}
	return b.Call, result, nil
}
`
