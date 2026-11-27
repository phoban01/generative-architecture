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
	// Optional fields: nil reads as the zero value.
	Suspend  *bool  `json:"suspend,omitempty"`
	Deadline *int64 `json:"deadline,omitempty"`
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

// Paused reads two optional fields: a nil test and a dereference of a
// *bool, and a nil test and a dereference of an *int64.
func (r *Reconciler) Paused(db *Database) bool {
	if db.Spec.Suspend != nil && *db.Spec.Suspend {
		return true
	}
	if db.Spec.Deadline == nil {
		return false
	}
	return *db.Spec.Deadline > 3
}
