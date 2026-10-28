// Package kube holds fixtures that use Kubernetes and controller-runtime
// types.
package kube

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	Scheme *runtime.Scheme
}

// Own makes the ConfigMap the controller of a new Secret.
func (r *Reconciler) Own(ctx context.Context, cm *corev1.ConfigMap) (reconcile.Result, error) {
	secret := &corev1.Secret{}
	secret.Name = cm.Name
	if err := controllerutil.SetControllerReference(cm, secret, r.Scheme); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// Refer adds a plain owner reference and returns its error.
func (r *Reconciler) Refer(ctx context.Context, cm *corev1.ConfigMap) (reconcile.Result, error) {
	secret := &corev1.Secret{}
	return reconcile.Result{}, controllerutil.SetOwnerReference(cm, secret, r.Scheme)
}

// Unblocked passes an option the summary does not cover.
func (r *Reconciler) Unblocked(ctx context.Context, cm *corev1.ConfigMap) (reconcile.Result, error) {
	secret := &corev1.Secret{}
	if err := controllerutil.SetControllerReference(cm, secret, r.Scheme, controllerutil.WithBlockOwnerDeletion(false)); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}
