// Package listing is a fixture for the List options the inventory records:
// one reconciler lists with a namespace and a field selector, one with an
// option the frontend does not record.
package listing

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const ownerKey = ".metadata.controller"

// Lister lists the ConfigMaps of the request's namespace that an index
// attributes to the request.
type Lister struct {
	Client client.Client
}

func (r *Lister) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	maps := &corev1.ConfigMapList{}
	if err := r.Client.List(ctx, maps, client.InNamespace(req.Namespace), client.MatchingFields{ownerKey: req.Name}); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// Limited lists with a page limit, which the frontend does not record.
type Limited struct {
	Client client.Client
}

func (r *Limited) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	maps := &corev1.ConfigMapList{}
	if err := r.Client.List(ctx, maps, client.Limit(5)); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}
