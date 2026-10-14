package typed

import "context"

// Database is a minimal object a Reconciler fetches and records. Its JSON
// tags follow the usual API conventions: an inlined embedded header, a
// tagged sub-struct and an unexported field that is not serialized.
type Database struct {
	TypeMeta `json:",inline"`
	Spec     DatabaseSpec `json:"spec"`
	internal int
}

type TypeMeta struct {
	Kind string `json:"kind,omitempty"`
}

type DatabaseSpec struct {
	Size int `json:"size"`
}

// ConfigMap carries a map field, which has no path lowering.
type ConfigMap struct {
	Data map[string]string `json:"data,omitempty"`
}

type Reconciler struct{}

func (r *Reconciler) get(ctx context.Context, key string) (*Database, error) { return nil, nil }
func (r *Reconciler) record(db *Database)                                    {}

// Fetch binds both results of a summarized call and tests the error.
func (r *Reconciler) Fetch(ctx context.Context, key string) (bool, error) {
	db, err := r.get(ctx, key)
	if err != nil {
		return false, err
	}
	r.record(db)
	return true, nil
}

// Exists discards the object with the blank identifier.
func (r *Reconciler) Exists(ctx context.Context, key string) (bool, error) {
	_, err := r.get(ctx, key)
	if err != nil {
		return false, err
	}
	return true, nil
}
