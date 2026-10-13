package assignment

import (
	context "context"
	apiv1 "generative-architecture/examples/machinevendor/api/v1alpha1"
	unstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	client "sigs.k8s.io/controller-runtime/pkg/client"
	handler "sigs.k8s.io/controller-runtime/pkg/handler"
	reconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// MAC is the controller-runtime shell around the process shown in controller.go.
// StoreFor binds a reconcile key to the application's actual I/O implementation.
type MAC struct {
	StoreFor func(context.Context, client.ObjectKey) Store
}

func (r *MAC) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	Reconcile(r.StoreFor(ctx, req.NamespacedName))
	return reconcile.Result{}, nil
}

// The generator reads this ordinary builder setup. No YAML duplicates the watches.
func (r *MAC) SetupWithManager(mgr ctrl.Manager) error {
	infra := &unstructured.Unstructured{}
	infra.SetGroupVersionKind(schema.GroupVersionKind{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "InfraMachine"})
	return ctrl.NewControllerManagedBy(mgr).
		For(infra).
		Watches(&apiv1.MachineClaim{}, handler.EnqueueRequestsFromMapFunc(ClaimToInfraMachine)).
		Complete(r)
}

// A claim can have a different name from its InfraMachine. Its reference supplies
// the reconcile key; status-only updates must also reach this mapping.
func ClaimToInfraMachine(_ context.Context, obj client.Object) []reconcile.Request {
	claim, ok := obj.(*apiv1.MachineClaim)
	if !ok || claim.Spec.InfraMachineName == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: claim.Namespace, Name: claim.Spec.InfraMachineName}}}
}
