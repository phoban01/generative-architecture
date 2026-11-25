// Package gadgetgen is a fixture for arch generate: a reconciler that reads a
// Widget through the typed client, with the API type from package v1beta1.
package gadgetgen

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1beta1 "github.com/phoban01/generative-architecture/cmd/arch/testdata/grouped/api/v1beta1"
)

type Reconciler struct {
	Client client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	w := &v1beta1.Widget{}
	if err := r.Client.Get(ctx, req.NamespacedName, w); err != nil {
		return reconcile.Result{}, err
	}
	w.Labels = map[string]string{"tier": "gold"}
	return reconcile.Result{}, r.Client.Update(ctx, w)
}
