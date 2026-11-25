// Package pair is a fixture for arch generate with two controllers. Mirror
// owns the ConfigMaps: it copies the source's value into the copy. Exporter
// owns the Secret and reads the copy into it, so its ConfigMap port binds
// to Mirror's.
package pair

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Mirror struct {
	Client    client.Client
	APIReader client.Reader
	Target    client.ObjectKey
}

func (r *Mirror) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	source := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, req.NamespacedName, source); err != nil {
		return reconcile.Result{}, err
	}
	target := &corev1.ConfigMap{}
	if err := r.APIReader.Get(ctx, r.Target, target); err != nil {
		return reconcile.Result{}, err
	}
	target.Data = map[string]string{"value": source.Data["value"]}
	return reconcile.Result{}, r.Client.Update(ctx, target)
}

func (r *Mirror) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Complete(r)
}

type Exporter struct {
	Client    client.Client
	APIReader client.Reader
	Source    client.ObjectKey
}

func (r *Exporter) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	secret := &corev1.Secret{}
	if err := r.Client.Get(ctx, req.NamespacedName, secret); err != nil {
		return reconcile.Result{}, err
	}
	copied := &corev1.ConfigMap{}
	if err := r.APIReader.Get(ctx, r.Source, copied); err != nil {
		return reconcile.Result{}, err
	}
	secret.StringData = map[string]string{"value": copied.Data["value"]}
	return reconcile.Result{}, r.Client.Update(ctx, secret)
}

func (r *Exporter) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		Complete(r)
}
