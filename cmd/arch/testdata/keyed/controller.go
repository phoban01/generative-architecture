// Package keyed is a fixture for arch generate: the selected field is a map
// key that is not a path identifier, which the abstraction quotes.
package keyed

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconciler copies the value under app.config/key from a source ConfigMap
// to a target ConfigMap.
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
	target.Data = map[string]string{"app.config/key": source.Data["app.config/key"]}
	return reconcile.Result{}, r.Client.Update(ctx, target)
}
