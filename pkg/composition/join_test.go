package composition_test

import (
	"github.com/phoban01/generative-architecture/pkg/composition"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/model"
	"reflect"
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
			diags:      []composition.Diagnostic{{Code: composition.DiagUnresolvedProvider, Port: "reader.configmap_v1", Message: "no component writes resource:/v1/ConfigMap/v1"}},
		},
		{
			name:       "several providers are ambiguous",
			components: []composition.Component{writer("b"), writer("a"), reader},
			diags:      []composition.Diagnostic{{Code: composition.DiagAmbiguousProvider, Port: "reader.configmap_v1", Message: "2 components write resource:/v1/ConfigMap/v1; add one explicit link to choose the provider", Candidates: []string{"a.configmap_v1", "b.configmap_v1"}}},
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
			diags:      []composition.Diagnostic{{Code: composition.DiagUnresolvedProvider, Port: "reader.configmap_v1", Message: "no component writes resource:/v1/ConfigMap/v1"}},
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
