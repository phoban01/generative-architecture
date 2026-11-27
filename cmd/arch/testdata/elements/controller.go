// Package elements is a fixture for arch generate: a reconciler that walks
// a list of objects, reads a field of each and deletes the ones that match.
package elements

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconciler deletes the stale maps of the request's namespace.
type Reconciler struct {
	Client client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	maps := &corev1.ConfigMapList{}
	if err := r.Client.List(ctx, maps, client.InNamespace(req.Namespace)); err != nil {
		return reconcile.Result{}, err
	}
	n := 0
	for _, cm := range maps.Items {
		if cm.Data["value"] != "stale" {
			continue
		}
		n = n + 1
		if err := r.Client.Delete(ctx, &cm); client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, err
		}
	}
	return reconcile.Result{}, nil
}
