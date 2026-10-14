package typed

// Sized reads tagged fields, including one promoted from the inlined header.
func (r *Reconciler) Sized(db *Database) bool {
	if db.Spec.Size > 0 && db.Kind == "Database" {
		return true
	}
	return false
}

// Value indexes a map, which has no field path.
func (r *Reconciler) Value(cm *ConfigMap) bool {
	if cm.Data["value"] == "" {
		return false
	}
	return true
}

// Hidden reads a field that is not serialized.
func (r *Reconciler) Hidden(db *Database) bool {
	if db.internal > 0 {
		return true
	}
	return false
}
