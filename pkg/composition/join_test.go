package composition_test

import (
	"github.com/phoban01/generative-architecture/pkg/composition"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/model"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func resourceComponent(name string, effects ...ir.Effect) composition.Component {
	return composition.Component{Name: name, Ports: composition.PortsFromEffects(name, effects)}
}

func TestJoinedResourcePortsCompose(t *testing.T) {
	spec := func(name string, update bool) model.Spec {
		s := model.Spec{Name: name, State: map[string]model.Variable{"configmap_objects": {Type: "int", Initial: "0"}, "seen": {Type: "int", Initial: "0"}}, Actions: []model.Action{{Name: "act", Guard: "true", Updates: map[string]expr.Expression{"seen": "configmap_objects"}}}}
		if update {
			s.Actions[0].Updates = map[string]expr.Expression{"configmap_objects": "configmap_objects + 1"}
		}
		return s
	}
	a := resourceComponent("a", apiEffect("create", ir.OpCreate, config, ir.Extracted))
	a.Model = spec("A", true)
	r := resourceComponent("reader", apiEffect("get", ir.OpGet, config, ir.Extracted))
	r.Model = spec("Reader", false)
	links, diags, err := composition.JoinByResource([]composition.Component{a, r}, nil, nil)
	if err != nil || len(diags) > 0 {
		t.Fatal(err, diags)
	}
	result, err := composition.Compose("Joined", []composition.Component{a, r}, links, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.StateNames["reader.configmap_objects"] != "a_configmap_objects" {
		t.Fatalf("not aliased: %v", result.StateNames)
	}
}

func TestJoinByResource(t *testing.T) {
	writer := func(name string) composition.Component {
		return resourceComponent(name, apiEffect("create", ir.OpCreate, config, ir.Extracted))
	}
	reader := resourceComponent("reader", apiEffect("get", ir.OpGet, config, ir.Extracted))
	for _, tc := range []struct {
		name       string
		components []composition.Component
		cluster    map[string]ir.ClusterID
		explicit   []composition.Link
		links      []composition.Link
		diags      []composition.Diagnostic
		err        string
	}{
		{
			name:       "unique provider joins",
			components: []composition.Component{writer("a"), reader},
			links:      []composition.Link{{From: "a.configmap_v1", To: "reader.configmap_v1"}},
		},
		{
			name:       "no provider is unresolved",
			components: []composition.Component{reader},
			diags:      []composition.Diagnostic{{Code: composition.DiagUnresolvedProvider, Port: "reader.configmap_v1", Message: "no component owns or creates resource:/v1/ConfigMap/v1; add a BoundaryModel that provides it or an explicit link"}},
		},
		{
			name:       "several providers are ambiguous",
			components: []composition.Component{writer("b"), writer("a"), reader},
			diags:      []composition.Diagnostic{{Code: composition.DiagAmbiguousProvider, Port: "reader.configmap_v1", Message: "2 components provide resource:/v1/ConfigMap/v1; add one explicit link to choose the provider", Candidates: []string{"a.configmap_v1", "b.configmap_v1"}}},
		},
		{
			name:       "explicit link suppresses ambiguity",
			components: []composition.Component{writer("b"), writer("a"), reader},
			explicit:   []composition.Link{{From: "b.configmap_v1", To: "reader.configmap_v1"}},
			links:      []composition.Link{{From: "b.configmap_v1", To: "reader.configmap_v1"}},
		},
		{
			name:       "cluster separates providers",
			components: []composition.Component{writer("b"), writer("a"), reader},
			cluster:    map[string]ir.ClusterID{"a": "east", "reader": "east", "b": "west"},
			links:      []composition.Link{{From: "a.configmap_v1", To: "reader.configmap_v1"}},
		},
		{
			name:       "provider in another cluster does not resolve",
			components: []composition.Component{writer("a"), reader},
			cluster:    map[string]ir.ClusterID{"a": "west"},
			diags:      []composition.Diagnostic{{Code: composition.DiagUnresolvedProvider, Port: "reader.configmap_v1", Message: "no component owns or creates resource:/v1/ConfigMap/v1; add a BoundaryModel that provides it or an explicit link"}},
		},
		{
			name:       "explicit resource link across clusters fails",
			components: []composition.Component{writer("a"), reader},
			cluster:    map[string]ir.ClusterID{"a": "west"},
			explicit:   []composition.Link{{From: "a.configmap_v1", To: "reader.configmap_v1"}},
			err:        "links[0] a.configmap_v1 -> reader.configmap_v1: resource binding crosses clusters",
		},
		{
			name:       "explicit link to unknown port fails",
			components: []composition.Component{reader},
			explicit:   []composition.Link{{From: "x.y", To: "reader.configmap_v1"}},
			err:        "links[0] x.y -> reader.configmap_v1: unknown port",
		},
		{
			name:       "unknown component in cluster map fails",
			components: []composition.Component{reader},
			cluster:    map[string]ir.ClusterID{"ghost": "east"},
			err:        "unknown component ghost",
		},
		{
			name:       "declared contracts are left to AutoLink",
			components: components(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			links, diags, err := composition.JoinByResource(tc.components, tc.cluster, tc.explicit)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(links, tc.links) && len(links)+len(tc.links) > 0 {
				t.Fatalf("links %v, want %v", links, tc.links)
			}
			if !reflect.DeepEqual(diags, tc.diags) {
				t.Fatalf("diags %v, want %v", diags, tc.diags)
			}
		})
	}
}

// TestJoinByOwnership covers the provider rule of ADR-0010.
func TestJoinByOwnership(t *testing.T) {
	owner := func(name string, effects ...ir.Effect) composition.Component {
		w := ir.Watch{ID: name, Controller: name, Resource: widget, Primary: true, Mapping: ir.KeyMapping{Kind: ir.MappingSelf}, Evidence: ir.Extracted, Provenance: prov(name + ".go")}
		return composition.Component{Name: name, Ports: composition.PortsFromIR(name, []ir.Watch{w}, effects)}
	}
	// A creator creates and then re-reads the resource, the usual pattern.
	creator := func(name string) composition.Component {
		return resourceComponent(name, apiEffect("create", ir.OpCreate, widget, ir.Extracted), apiEffect("get", ir.OpGet, widget, ir.Extracted))
	}
	reader := resourceComponent("reader", apiEffect("get", ir.OpGet, widget, ir.Extracted))
	patcher := resourceComponent("patcher", apiEffect("patch", ir.OpPatch, widget, ir.Extracted))
	const port = "widget_v1_example_org"
	const unresolved = "no component owns or creates resource:example.org/v1/Widget/v1; add a BoundaryModel that provides it or an explicit link"
	for _, tc := range []struct {
		name       string
		components []composition.Component
		links      []composition.Link
		diags      []composition.Diagnostic
		// writableIn names the components whose port resolves to a writable in port.
		writableIn []string
	}{
		{
			name:       "primary owner provides and the creator gets a writable in port",
			components: []composition.Component{creator("creator"), owner("ctrl", apiEffect("status", ir.OpUpdate, widget, ir.Extracted)), reader},
			links:      []composition.Link{{From: "ctrl." + port, To: "creator." + port}, {From: "ctrl." + port, To: "reader." + port}},
			writableIn: []string{"creator"},
		},
		{
			name:       "sole creator provides without an owner",
			components: []composition.Component{creator("creator"), patcher, reader},
			links:      []composition.Link{{From: "creator." + port, To: "patcher." + port}, {From: "creator." + port, To: "reader." + port}},
			writableIn: []string{"patcher"},
		},
		{
			name:       "no owner and no creator is unresolved",
			components: []composition.Component{patcher, reader},
			diags: []composition.Diagnostic{
				{Code: composition.DiagUnresolvedProvider, Port: "patcher." + port, Message: unresolved},
				{Code: composition.DiagUnresolvedProvider, Port: "reader." + port, Message: unresolved},
			},
			writableIn: []string{"patcher"},
		},
		{
			name:       "two primary owners are ambiguous",
			components: []composition.Component{owner("b"), owner("a"), creator("creator")},
			diags:      []composition.Diagnostic{{Code: composition.DiagAmbiguousProvider, Port: "creator." + port, Message: "2 components provide resource:example.org/v1/Widget/v1; add one explicit link to choose the provider", Candidates: []string{"a." + port, "b." + port}}},
			writableIn: []string{"creator"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			links, diags, err := composition.JoinByResource(tc.components, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(links, tc.links) {
				t.Errorf("links %v, want %v", links, tc.links)
			}
			if !reflect.DeepEqual(diags, tc.diags) {
				t.Errorf("diags %v, want %v", diags, tc.diags)
			}
			resolved := composition.ResolveProviders(tc.components, nil)
			if again := composition.ResolveProviders(resolved, nil); !reflect.DeepEqual(again, resolved) {
				t.Error("ResolveProviders is not idempotent")
			}
			for _, c := range resolved {
				if !slices.Contains(tc.writableIn, c.Name) {
					continue
				}
				if p := c.Ports[port]; p.Direction != "in" || !p.Writable {
					t.Errorf("%s port %+v, want a writable in port", c.Name, p)
				}
			}
		})
	}
}
