// Package requeue is a fixture for arch generate: a reconciler that asks for
// a delayed requeue when its source has no value yet.
package requeue

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconciler copies the value of a source ConfigMap into a target ConfigMap
// once the source has one.
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
	if source.Data["value"] == "" {
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	target := &corev1.ConfigMap{}
	if err := r.APIReader.Get(ctx, r.Target, target); err != nil {
		return reconcile.Result{}, err
	}
	target.Data = map[string]string{"value": source.Data["value"]}
	return reconcile.Result{}, r.Client.Update(ctx, target)
}
