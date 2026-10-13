// Package configmirror is an independent consumer of the public library. Its
// controller is ordinary Go: no architecture annotations or test hooks required.
package configmirror

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	Client    client.Client
	APIReader client.Reader
	Target    client.ObjectKey
}

// Reconcile copies one ConfigMap value to a pre-existing target. Reads of the
// source use the injected cache; target reads and writes go to the API.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	source := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, req.NamespacedName, source); err != nil {
		return reconcile.Result{}, err
	}
	target := &corev1.ConfigMap{}
	if err := r.APIReader.Get(ctx, r.Target, target); err != nil {
		return reconcile.Result{}, err
	}
	target.Data = map[string]string{"value": source.Data["value"]}
	return reconcile.Result{}, r.Client.Update(ctx, target)
}
