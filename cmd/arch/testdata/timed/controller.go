// Package timed is a fixture for arch generate: a reconciler that reads a
// clock and takes a decision on it, which the model treats as a choice.
package timed

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Clock reads the current time; tests inject one.
type Clock interface {
	Now() time.Time
}

// Reconciler marks the target late once the deadline has passed.
type Reconciler struct {
	Client    client.Client
	APIReader client.Reader
	Target    client.ObjectKey
	Deadline  time.Time
	Clock
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	source := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, req.NamespacedName, source); err != nil {
		return reconcile.Result{}, err
	}
	logger.Info("reconciling", "now", r.Now())
	late := r.Now().After(r.Deadline)
	target := &corev1.ConfigMap{}
	if err := r.APIReader.Get(ctx, r.Target, target); err != nil {
		return reconcile.Result{}, err
	}
	if late && source.Data["value"] == "a" {
		target.Data = map[string]string{"value": "a"}
	}
	return reconcile.Result{}, r.Client.Update(ctx, target)
}
