package typed

import (
	"context"
	"time"
)

// Delay is a delay the constant folder can read.
const Delay = 5 * time.Second

// RequeueLater asks for a delayed requeue, or for an immediate one when the
// write fails.
func (r *Reconciler) RequeueLater(ctx context.Context) (Result, error) {
	if err := r.write(ctx, &ConfigMap{}); err != nil {
		return Result{Requeue: true}, nil
	}
	return Result{RequeueAfter: Delay}, nil
}

// RequeueBoth sets both fields; the delay wins.
func (r *Reconciler) RequeueBoth(ctx context.Context) (Result, error) {
	return Result{Requeue: true, RequeueAfter: time.Minute}, nil
}

// RequeueNone sets both fields to their zero values.
func (r *Reconciler) RequeueNone(ctx context.Context) (Result, error) {
	return Result{Requeue: false, RequeueAfter: 0}, nil
}

// RequeueVariable decides at run time whether to requeue.
func (r *Reconciler) RequeueVariable(ctx context.Context, again bool) (Result, error) {
	return Result{Requeue: again}, nil
}

// RequeueByPosition sets the fields by position.
func (r *Reconciler) RequeueByPosition(ctx context.Context) (Result, error) {
	return Result{true, 0}, nil
}

// RequeueSoon waits for ready with a delayed requeue.
func (r *Reconciler) RequeueSoon(ctx context.Context, ready bool) (Result, error) {
	if ready {
		return Result{}, nil
	}
	return Result{RequeueAfter: Delay}, nil
}

// RequeueLocal builds the result once and returns the local twice, as the
// kubebuilder CronJob controller does.
func (r *Reconciler) RequeueLocal(ctx context.Context) (Result, error) {
	scheduled := Result{RequeueAfter: Delay}
	if err := r.write(ctx, &ConfigMap{}); err != nil {
		return scheduled, nil
	}
	return scheduled, nil
}

// RequeueLocalChanged changes the local after it is defined.
func (r *Reconciler) RequeueLocalChanged(ctx context.Context, again bool) (Result, error) {
	scheduled := Result{RequeueAfter: Delay}
	scheduled = Result{Requeue: again}
	return scheduled, nil
}
