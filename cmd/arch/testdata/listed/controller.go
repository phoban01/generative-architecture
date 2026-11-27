// Package listed is a fixture for arch generate: a reconciler that lists
// the ConfigMaps of the request's namespace and acts on their number.
package listed

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconciler marks the target when the namespace holds more than one map.
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
	maps := &corev1.ConfigMapList{}
	if err := r.Client.List(ctx, maps, client.InNamespace(req.Namespace)); err != nil {
		return reconcile.Result{}, err
	}
	target := &corev1.ConfigMap{}
	if err := r.APIReader.Get(ctx, r.Target, target); err != nil {
		return reconcile.Result{}, err
	}
	if len(maps.Items) > 1 && source.Data["value"] == "a" {
		target.Data = map[string]string{"value": "a"}
	}
	return reconcile.Result{}, r.Client.Update(ctx, target)
}
