package database

import (
	context "context"
	apiv1 "github.com/phoban01/generative-architecture/examples/database/api/v1alpha1"
	unstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	client "sigs.k8s.io/controller-runtime/pkg/client"
	handler "sigs.k8s.io/controller-runtime/pkg/handler"
	reconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// DatabaseReconciler is the controller-runtime shell around the process shown in controller.go.
// StoreFor binds a reconcile key to the application's actual I/O implementation.
type DatabaseReconciler struct {
	StoreFor func(context.Context, client.ObjectKey) Store
}

func (r *DatabaseReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	Reconcile(r.StoreFor(ctx, req.NamespacedName))
	return reconcile.Result{}, nil
}

// The generator reads this ordinary builder setup. No YAML duplicates the watches.
func (r *DatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	database := &unstructured.Unstructured{}
	database.SetGroupVersionKind(schema.GroupVersionKind{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "Database"})
	return ctrl.NewControllerManagedBy(mgr).
		For(database).
		Watches(&apiv1.DatabaseInstance{}, handler.EnqueueRequestsFromMapFunc(InstanceToDatabase)).
		Complete(r)
}

// A instance can have a different name from its Database. Its reference supplies
// the reconcile key; status-only updates must also reach this mapping.
func InstanceToDatabase(_ context.Context, obj client.Object) []reconcile.Request {
	instance, ok := obj.(*apiv1.DatabaseInstance)
	if !ok || instance.Spec.DatabaseName == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: instance.Namespace, Name: instance.Spec.DatabaseName}}}
}
