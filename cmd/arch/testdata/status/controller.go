// Package status is a fixture for arch generate: a reconciler that updates
// the status subresource of a Namespace.
package status

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconciler marks the Namespace the request names active.
type Reconciler struct {
	Client client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ns := &corev1.Namespace{}
	if err := r.Client.Get(ctx, req.NamespacedName, ns); err != nil {
		return reconcile.Result{}, err
	}
	ns.Status.Phase = corev1.NamespaceActive
	return reconcile.Result{}, r.Client.Status().Update(ctx, ns)
}
