// Package widget is a fixture for arch extract: a reconciler of a kind
// whose API package name differs from its version.
package widget

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/phoban01/generative-architecture/cmd/arch/testdata/grouped/api/widgets"
)

type Reconciler struct {
	Client client.Client
}

func (r *Reconciler) Reconcile(context.Context, reconcile.Request) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&widgets.Widget{}).
		Complete(r)
}
