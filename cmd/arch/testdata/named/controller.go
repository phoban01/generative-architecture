// Package named is a fixture for arch generate: a reconciler that reads a
// second object by a key literal built from the first object's name.
package named

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconciler creates a Namespace named after the ConfigMap the request
// names.
type Reconciler struct {
	Client client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	cm := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, req.NamespacedName, cm); err != nil {
		return reconcile.Result{}, err
	}
	ns := &corev1.Namespace{}
	err := r.Client.Get(ctx, client.ObjectKey{Name: cm.Name}, ns)
	if apierrors.IsNotFound(err) {
		ns.Name = cm.Name
		return reconcile.Result{}, r.Client.Create(ctx, ns)
	}
	return reconcile.Result{}, err
}
