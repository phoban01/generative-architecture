// Package fields is a fixture for arch generate: a resource with two
// selected fields, both copied from a source ConfigMap to a target.
package fields

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconciler copies the value and the mode of a source ConfigMap to a target.
type Reconciler struct {
	Client    client.Client
	APIReader client.Reader
	Target    client.ObjectKey
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	source := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, req.NamespacedName, source); err != nil {
		return reconcile.Result{}, err
	}
	target := &corev1.ConfigMap{}
	if err := r.APIReader.Get(ctx, r.Target, target); err != nil {
		return reconcile.Result{}, err
	}
	target.Data = map[string]string{"value": source.Data["value"], "mode": source.Data["mode"]}
	return reconcile.Result{}, r.Client.Update(ctx, target)
}
