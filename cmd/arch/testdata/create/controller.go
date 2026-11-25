// Package create is a fixture for arch generate: a reconciler that creates
// a ConfigMap when it is absent.
package create

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconciler ensures the ConfigMap the request names exists with a value.
type Reconciler struct {
	Client client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	cm := &corev1.ConfigMap{}
	err := r.Client.Get(ctx, req.NamespacedName, cm)
	if apierrors.IsNotFound(err) {
		cm.Data = map[string]string{"value": "a"}
		return reconcile.Result{}, r.Client.Create(ctx, cm)
	}
	return reconcile.Result{}, err
}
