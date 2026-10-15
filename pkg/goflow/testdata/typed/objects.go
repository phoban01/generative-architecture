package typed

import "context"

// Result stands for a reconcile result whose zero value means no requeue.
type Result struct {
	Requeue bool
}

func (r *Reconciler) read(ctx context.Context, key string, cm *ConfigMap) error  { return nil }
func (r *Reconciler) write(ctx context.Context, cm *ConfigMap) error             { return nil }
func (r *Reconciler) after(ctx context.Context, key string, cm *ConfigMap) error { return nil }

// Copy declares zero-valued objects, copies a map entry between them and
// returns the error of the final write directly.
func (r *Reconciler) Copy(ctx context.Context, from, to string) (Result, error) {
	source := &ConfigMap{}
	if err := r.read(ctx, from, source); err != nil {
		return Result{}, err
	}
	target := ConfigMap{}
	target.Data = map[string]string{"value": source.Data["value"]}
	return Result{}, r.write(ctx, &target)
}

// Requeue returns a result that is not the zero value.
func (r *Reconciler) Requeue(ctx context.Context) (Result, error) {
	return Result{Requeue: true}, nil
}

// Unsummarized returns the error of a call nothing summarizes.
func (r *Reconciler) Unsummarized(ctx context.Context, key string) (Result, error) {
	return Result{}, r.after(ctx, key, &ConfigMap{})
}
