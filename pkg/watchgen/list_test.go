package watchgen_test

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/watchgen"
)

// listSource is a reconciler whose Parent watch maps through a method that
// lists Child objects. MAPPER is replaced with the method body.
const listSource = `package demo

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "example.org/demo/api/v1"
)

const ParentLabel = "example.org/parent"

type ChildReconciler struct {
	Client client.Client
}

func (r *ChildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Child{}).
		Watches(&v1.Parent{}, handler.EnqueueRequestsFromMapFunc(r.parentToChildren)).
		Complete(r)
}

func (r *ChildReconciler) parentToChildren(ctx context.Context, obj client.Object) []reconcile.Request {
MAPPER
}
`

const labelBody = `	children := &v1.ChildList{}
	if err := r.Client.List(ctx, children, client.InNamespace(obj.GetNamespace()), client.MatchingLabels{ParentLabel: obj.GetName()}); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(children.Items))
	for _, c := range children.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: c.Namespace, Name: c.Name}})
	}
	return requests`

const referenceBody = `	children := &v1.ChildList{}
	if err := r.Client.List(ctx, children, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for _, c := range children.Items {
		if c.Spec.Parent == obj.GetName() {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: c.Namespace, Name: c.Name}})
		}
	}
	return requests`

const fieldBody = `	p, ok := obj.(*v1.Parent)
	if !ok || p.Spec.Child == "" { return nil }
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: p.Namespace, Name: p.Spec.Child}}}`

func listResources() map[string]effect.Resource {
	return map[string]effect.Resource{
		"example.org/demo/api/v1.Child":  {Group: "demo.example", Version: "v1", Kind: "Child"},
		"example.org/demo/api/v1.Parent": {Group: "demo.example", Version: "v1", Kind: "Parent"},
	}
}

func TestListMappers(t *testing.T) {
	child := &ir.GVK{Group: "demo.example", Version: "v1", Kind: "Child"}
	tests := []struct {
		name string
		body string
		want ir.KeyMapping
	}{
		{"label selector", labelBody, ir.KeyMapping{Kind: ir.MappingLabel, Label: "example.org/parent", Dependent: child}},
		{"literal label key", strings.Replace(labelBody, "ParentLabel:", `"example.org/other":`, 1), ir.KeyMapping{Kind: ir.MappingLabel, Label: "example.org/other", Dependent: child}},
		{"label with var requests", strings.Replace(labelBody, "requests := make([]reconcile.Request, 0, len(children.Items))", "var requests []reconcile.Request", 1), ir.KeyMapping{Kind: ir.MappingLabel, Label: "example.org/parent", Dependent: child}},
		{"field reference", referenceBody, ir.KeyMapping{Kind: ir.MappingReference, DependentPath: []string{"Spec", "Parent"}, Dependent: child}},
		{"field method", fieldBody, ir.KeyMapping{Kind: ir.MappingField, FieldPath: []string{"Spec", "Child"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := strings.Replace(listSource, "MAPPER", tt.body, 1)
			result, err := watchgen.Extract([]byte(source), "ChildReconciler", "SetupWithManager", listResources())
			if err != nil {
				t.Fatal(err)
			}
			watches := result.IR("child", "controller.go")
			if len(watches) != 2 {
				t.Fatalf("watches %+v", watches)
			}
			got := watches[1].Mapping
			if got.Kind != tt.want.Kind || got.Label != tt.want.Label || strings.Join(got.DependentPath, ".") != strings.Join(tt.want.DependentPath, ".") ||
				strings.Join(got.FieldPath, ".") != strings.Join(tt.want.FieldPath, ".") || (got.Dependent == nil) != (tt.want.Dependent == nil) ||
				(got.Dependent != nil && *got.Dependent != *tt.want.Dependent) {
				t.Fatalf("mapping %+v, want %+v", got, tt.want)
			}
			if err := watches[1].Validate(); err != nil {
				t.Fatal(err)
			}
			if result.Rules[1].MappingSHA256 == "" {
				t.Fatal("mapping hash missing")
			}
		})
	}
}

func TestUnsupportedListMappersAreLocated(t *testing.T) {
	tests := []struct {
		name, source, construct, reason string
	}{
		{"other receiver", strings.Replace(strings.Replace(listSource, "MAPPER", labelBody, 1), "EnqueueRequestsFromMapFunc(r.parentToChildren)", "EnqueueRequestsFromMapFunc(other.parentToChildren)", 1),
			"handler", "mapping must name a local function or a method of the receiver"},
		{"missing method", strings.Replace(strings.Replace(listSource, "MAPPER", labelBody, 1), "r.parentToChildren", "r.missing", 1),
			"mapper", "unsupported mapping function missing"},
		{"two labels", strings.Replace(strings.Replace(listSource, "MAPPER", labelBody, 1), "obj.GetName()}", `obj.GetName(), "b": "c"}`, 1),
			"mapper", "list selector must be client.MatchingLabels with one label"},
		{"computed label key", strings.Replace(strings.Replace(listSource, "MAPPER", labelBody, 1), "ParentLabel:", "r.Key:", 1),
			"mapper", "label key must be a string constant"},
		{"other label value", strings.Replace(strings.Replace(listSource, "MAPPER", labelBody, 1), "ParentLabel: obj.GetName()", `ParentLabel: "fixed"`, 1),
			"mapper", "unsupported mapping control flow in parentToChildren"},
		{"other namespace", strings.Replace(strings.Replace(listSource, "MAPPER", labelBody, 1), "client.InNamespace(obj.GetNamespace())", `client.InNamespace("kube-system")`, 1),
			"mapper", "unsupported mapping control flow in parentToChildren"},
		{"unknown list kind", strings.Replace(strings.Replace(listSource, "MAPPER", labelBody, 1), "v1.ChildList", "v1.OtherList", 1),
			"mapper", "unknown listed resource example.org/demo/api/v1.OtherList"},
		{"no selector", strings.Replace(strings.Replace(listSource, "MAPPER", labelBody, 1), ", client.MatchingLabels{ParentLabel: obj.GetName()}", "", 1),
			"mapper", "list mapper must select by a label or a field"},
		{"filter not equal", strings.Replace(strings.Replace(listSource, "MAPPER", referenceBody, 1), "c.Spec.Parent == obj.GetName()", "c.Spec.Parent != obj.GetName()", 1),
			"mapper", "list filter must compare an item field with the object name"},
		{"filter on namespace", strings.Replace(strings.Replace(listSource, "MAPPER", referenceBody, 1), "c.Spec.Parent == obj.GetName()", "c.Spec.Parent == obj.GetNamespace()", 1),
			"mapper", "unsupported mapping control flow in parentToChildren"},
		{"list through package client", strings.Replace(strings.Replace(listSource, "MAPPER", labelBody, 1), "r.Client.List", "global.List", 1),
			"mapper", "list mapper must list through a client field of the receiver"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := watchgen.Extract([]byte(tt.source), "ChildReconciler", "SetupWithManager", listResources())
			d, ok := goflow.AsDiagnostic(err)
			if !ok {
				t.Fatalf("expected a diagnostic, got %v", err)
			}
			if d.Construct != tt.construct || d.Reason != tt.reason || d.Line == 0 {
				t.Fatalf("diagnostic %+v, want %s: %s", d, tt.construct, tt.reason)
			}
		})
	}
}
