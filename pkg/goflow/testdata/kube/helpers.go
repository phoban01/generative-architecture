package kube

import (
	"context"
	"sort"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *Reconciler) read(ctx context.Context, cm *corev1.ConfigMap) error { return nil }

// OwnAlias sets the controller reference through controller-runtime's
// package variable alias of controllerutil.SetControllerReference.
func (r *Reconciler) OwnAlias(ctx context.Context, cm *corev1.ConfigMap) (reconcile.Result, error) {
	secret := &corev1.Secret{}
	if err := ctrl.SetControllerReference(cm, secret, r.Scheme); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// IgnoreMissing returns the error of a read unless the object is missing.
func (r *Reconciler) IgnoreMissing(ctx context.Context, cm *corev1.ConfigMap) (reconcile.Result, error) {
	if err := r.read(ctx, cm); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	return reconcile.Result{}, nil
}

// IgnoreMissingTest tests the filtered error in a condition.
func (r *Reconciler) IgnoreMissingTest(ctx context.Context, cm *corev1.ConfigMap) (reconcile.Result, error) {
	if err := r.read(ctx, cm); client.IgnoreNotFound(err) != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// IgnoreParam filters an error nothing summarized.
func (r *Reconciler) IgnoreParam(ctx context.Context, err error) (reconcile.Result, error) {
	return reconcile.Result{}, client.IgnoreNotFound(err)
}

// Tidy sorts the jobs by start time, as the kubebuilder CronJob controller
// does. Sorting a local slice has no model effect.
func (r *Reconciler) Tidy(ctx context.Context, jobs []*batchv1.Job) (reconcile.Result, error) {
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Status.StartTime == nil {
			return jobs[j].Status.StartTime != nil
		}
		return jobs[i].Status.StartTime.Before(jobs[j].Status.StartTime)
	})
	return reconcile.Result{}, nil
}

// TidyCall hides a call with an effect inside the comparator.
func (r *Reconciler) TidyCall(ctx context.Context, jobs []*batchv1.Job) (reconcile.Result, error) {
	sort.Slice(jobs, func(i, j int) bool { return r.owner(nil) < jobs[i].Name })
	return reconcile.Result{}, nil
}
