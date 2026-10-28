// Package inventory is a fixture for the Reconcile inventory of arch
// extract: one reconciler the frontend reads whole, and one with a
// construct it cannot read.
package inventory

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Mirror copies one value of a ConfigMap into a Secret.
type Mirror struct {
	Client client.Client
}

func (r *Mirror) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	cm := &corev1.ConfigMap{}
	err := r.Client.Get(ctx, req.NamespacedName, cm)
	if apierrors.IsNotFound(err) {
		return reconcile.Result{}, nil
	}
	if err != nil {
		return reconcile.Result{}, err
	}
	if cm.Data["a"] == "" {
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	secret := &corev1.Secret{}
	secret.Namespace = cm.Namespace
	secret.Name = cm.Name
	secret.StringData = map[string]string{"a": cm.Data["a"]}
	return reconcile.Result{}, r.Client.Create(ctx, secret)
}

// Printer prints, which the frontend cannot read.
type Printer struct {
	Client client.Client
}

func (r *Printer) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	fmt.Println(req.Name)
	return reconcile.Result{}, nil
}
