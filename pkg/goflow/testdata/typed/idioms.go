package typed

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Ensure fetches an object and distinguishes a missing one from a failure.
func (r *Reconciler) Ensure(ctx context.Context, key string) (string, error) {
	db, err := r.get(ctx, key)
	if apierrors.IsNotFound(err) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	r.record(db)
	return "ready", nil
}

// Create reports a conflict through the negated predicate form.
func (r *Reconciler) Create(ctx context.Context, key string) (string, error) {
	_, err := r.get(ctx, key)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return "", err
	}
	return "created", nil
}

// Probe applies a predicate to an error no summary produced.
func (r *Reconciler) Probe(ctx context.Context, key string) (string, error) {
	if apierrors.IsForbidden(ctx.Err()) {
		return "forbidden", nil
	}
	return "ok", nil
}
