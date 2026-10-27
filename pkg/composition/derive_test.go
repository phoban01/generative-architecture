package composition_test

import (
	"generative-architecture/pkg/composition"
	"generative-architecture/pkg/ir"
	"reflect"
	"sort"
	"testing"
)

func prov(file string) ir.Provenance {
	return ir.Provenance{Generator: "test", Source: ir.Source{File: file, SHA256: "00"}}
}

func apiEffect(id, op string, g ir.GVK, e ir.Evidence) ir.Effect {
	return ir.Effect{ID: id, Actor: "worker", Resource: g, Operation: op, Consistency: ir.ConsistencyCached, Evidence: e, Provenance: prov(id + ".go")}
}

var (
	widget = ir.GVK{Group: "example.org", Version: "v1", Kind: "Widget"}
	config = ir.GVK{Version: "v1", Kind: "ConfigMap"}
	child  = ir.GVK{Group: "example.org", Version: "v1", Kind: "Child"}
)

func TestPortsFromEffects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		effects []ir.Effect
		watches []ir.Watch
		want    map[string]composition.Port
	}{
		{
			name: "read, write and read-write resources",
			effects: []ir.Effect{
				apiEffect("getWidget", ir.OpGet, widget, ir.Extracted),
				apiEffect("createConfig", ir.OpCreate, config, ir.Extracted),
				apiEffect("listChild", ir.OpList, child, ir.Extracted),
				apiEffect("patchChild", ir.OpPatch, child, ir.Declared),
			},
			want: map[string]composition.Port{
				"widget_v1_example_org": {State: "widget_objects", Contract: "resource:example.org/v1/Widget", Version: "v1", Direction: "in", Evidence: ir.Extracted, Provenance: []ir.Provenance{prov("getWidget.go")}},
				"configmap_v1":          {State: "configmap_objects", Contract: "resource:/v1/ConfigMap", Version: "v1", Direction: "out", Evidence: ir.Extracted, Provenance: []ir.Provenance{prov("createConfig.go")}},
				"child_v1_example_org":  {State: "child_objects", Contract: "resource:example.org/v1/Child", Version: "v1", Direction: "in", Writable: true, Evidence: ir.Extracted, Provenance: []ir.Provenance{prov("listChild.go"), prov("patchChild.go")}},
			},
		},
		{
			name:    "declared effect stays declared",
			effects: []ir.Effect{apiEffect("updateConfig", ir.OpUpdate, config, ir.Declared)},
			want: map[string]composition.Port{
				"configmap_v1": {State: "configmap_objects", Contract: "resource:/v1/ConfigMap", Version: "v1", Direction: "out", Evidence: ir.Declared, Provenance: []ir.Provenance{prov("updateConfig.go")}},
			},
		},
		{
			name: "schema and external effects yield no port",
			effects: []ir.Effect{
				{ID: "schema", Actor: "worker", Resource: config, Operation: ir.OpGet, Consistency: ir.ConsistencySchema, Evidence: ir.Extracted},
				{ID: "call", Actor: "worker", Kind: ir.KindExternal, Target: &ir.Callee{Package: "p", Func: "F"}, Outcomes: []ir.Outcome{{Name: "ok"}}, Evidence: ir.Declared},
			},
			want: map[string]composition.Port{},
		},
		{
			name:    "watch reads and a write makes it writable",
			watches: []ir.Watch{{ID: "w", Controller: "worker", Resource: widget, Mapping: ir.KeyMapping{Kind: ir.MappingSelf}, Evidence: ir.Extracted, Provenance: prov("watch.go")}},
			effects: []ir.Effect{apiEffect("statusWidget", ir.OpUpdate, widget, ir.Extracted)},
			want: map[string]composition.Port{
				"widget_v1_example_org": {State: "widget_objects", Contract: "resource:example.org/v1/Widget", Version: "v1", Direction: "in", Writable: true, Evidence: ir.Extracted, Provenance: []ir.Provenance{prov("statusWidget.go"), prov("watch.go")}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := composition.PortsFromIR("worker", tc.watches, tc.effects)
			if tc.watches == nil {
				if alt := composition.PortsFromEffects("worker", tc.effects); !reflect.DeepEqual(alt, got) {
					t.Fatalf("PortsFromEffects differs: %v", alt)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestMergePorts(t *testing.T) {
	derived := composition.PortsFromEffects("worker", []ir.Effect{apiEffect("getWidget", ir.OpGet, widget, ir.Extracted)})
	for _, tc := range []struct {
		name     string
		declared map[string]composition.Port
		ports    []string
		diags    []string
	}{
		{"none", nil, []string{"widget_v1_example_org"}, nil},
		{"distinct", map[string]composition.Port{"data": {State: "x", Contract: "data", Version: "v1", Direction: "out"}}, []string{"data", "widget_v1_example_org"}, nil},
		{"same contract", map[string]composition.Port{"widgets": {State: "x", Contract: "resource:example.org/v1/Widget", Version: "v1", Direction: "in"}}, []string{"widget_v1_example_org"}, []string{"worker.widgets"}},
		{"same name", map[string]composition.Port{"widget_v1_example_org": {State: "x", Contract: "other", Version: "v1", Direction: "in"}}, []string{"widget_v1_example_org"}, []string{"worker.widget_v1_example_org"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ports, diags := composition.MergePorts("worker", tc.declared, derived)
			var names, located []string
			for n := range ports {
				names = append(names, n)
			}
			for _, d := range diags {
				if d.Code != composition.DiagDuplicatePort {
					t.Fatalf("code %q", d.Code)
				}
				located = append(located, d.Port)
			}
			sort.Strings(names)
			if !reflect.DeepEqual(names, tc.ports) || !reflect.DeepEqual(located, tc.diags) {
				t.Fatalf("ports %v diags %v", names, diags)
			}
			if ports["widget_v1_example_org"].Evidence != ir.Extracted {
				t.Fatal("derived port replaced")
			}
		})
	}
}
