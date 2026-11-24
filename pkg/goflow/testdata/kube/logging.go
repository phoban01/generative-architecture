package kube

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Log logs in the shapes of the kubebuilder CronJob controller.
func (r *Reconciler) Log(ctx context.Context, cm *corev1.ConfigMap, items []string, ready bool) (reconcile.Result, error) {
	log := logf.FromContext(ctx)
	log = log.WithValues("name", cm.Name)
	if ready {
		log.V(1).Info("items", "count", len(items), "first", string(items[0]))
		return reconcile.Result{}, nil
	}
	log.Error(nil, "no items")
	return reconcile.Result{}, nil
}

// LogCall hides a call with an effect inside a log argument.
func (r *Reconciler) LogCall(ctx context.Context, cm *corev1.ConfigMap) (reconcile.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("owner", "ref", r.owner(cm))
	return reconcile.Result{}, nil
}

func (r *Reconciler) owner(cm *corev1.ConfigMap) string { return "" }
