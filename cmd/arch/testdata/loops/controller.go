// Package loops is a fixture for arch generate: a reconciler that builds a
// list of identities, ranges over it and writes a result that depends on
// the count.
package loops

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconciler marks the target active when the source is.
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
	var active []string
	if source.Data["value"] == "a" {
		active = append(active, "source")
	}
	n := 0
	for _, id := range active {
		if id != "source" {
			continue
		}
		n = n + 1
	}
	target := &corev1.ConfigMap{}
	if err := r.APIReader.Get(ctx, r.Target, target); err != nil {
		return reconcile.Result{}, err
	}
	if n > 0 {
		target.Data = map[string]string{"value": "a"}
	}
	return reconcile.Result{}, r.Client.Update(ctx, target)
}
