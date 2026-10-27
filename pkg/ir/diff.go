package ir

import (
	"reflect"
	"sort"
	"strings"
)

// Record kinds named in a Delta.
const (
	RecordController = "controller"
	RecordWatch      = "watch"
	RecordEffect     = "effect"
	RecordProcess    = "process"
	RecordContract   = "contract"
	RecordConstraint = "constraint"
)

// Change is one record that differs between two documents. Fields lists
// the YAML names of the top-level fields that differ; it is set only for a
// changed record.
type Change struct {
	Kind   string   `json:"kind"`
	ID     string   `json:"id"`
	Fields []string `json:"fields,omitempty"`
}

// Delta is the semantic difference between two documents. Records are
// matched by ID. A changed record has the same ID and different content.
type Delta struct {
	Added   []Change `json:"added"`
	Removed []Change `json:"removed"`
	Changed []Change `json:"changed"`
}

// Empty reports whether the documents have the same records.
func (d Delta) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// Diff compares the controllers, watches, effects, processes, contract
// implementations and constraints of two documents. Provenance is not
// content: a record that only moved in its file is not changed. Each list
// in the result is sorted by kind and ID.
func Diff(old, new Document) Delta {
	var d Delta
	d.compare(RecordController, index(old.Controllers, func(c Controller) string { return c.ID }), index(new.Controllers, func(c Controller) string { return c.ID }))
	d.compare(RecordWatch, index(old.Watches, func(w Watch) string { return w.ID }), index(new.Watches, func(w Watch) string { return w.ID }))
	d.compare(RecordEffect, index(old.Effects, func(e Effect) string { return e.ID }), index(new.Effects, func(e Effect) string { return e.ID }))
	d.compare(RecordProcess, index(old.Processes, func(p Process) string { return p.ID }), index(new.Processes, func(p Process) string { return p.ID }))
	d.compare(RecordContract, index(old.Contracts, ContractImplementation.ID), index(new.Contracts, ContractImplementation.ID))
	d.compare(RecordConstraint, index(old.Constraints, func(c Constraint) string { return c.ID }), index(new.Constraints, func(c Constraint) string { return c.ID }))
	for _, list := range [][]Change{d.Added, d.Removed, d.Changed} {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Kind != list[j].Kind {
				return list[i].Kind < list[j].Kind
			}
			return list[i].ID < list[j].ID
		})
	}
	return d
}

func index[T any](records []T, id func(T) string) map[string]any {
	out := make(map[string]any, len(records))
	for _, r := range records {
		out[id(r)] = r
	}
	return out
}

func (d *Delta) compare(kind string, old, new map[string]any) {
	for id, o := range old {
		n, ok := new[id]
		if !ok {
			d.Removed = append(d.Removed, Change{Kind: kind, ID: id})
			continue
		}
		if fields := changedFields(o, n); len(fields) > 0 {
			d.Changed = append(d.Changed, Change{Kind: kind, ID: id, Fields: fields})
		}
	}
	for id := range new {
		if _, ok := old[id]; !ok {
			d.Added = append(d.Added, Change{Kind: kind, ID: id})
		}
	}
}

// changedFields lists the YAML names of the struct fields that differ
// between a and b, two values of the same record type, skipping
// provenance.
func changedFields(a, b any) []string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	t := va.Type()
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Type == reflect.TypeOf(Provenance{}) {
			continue
		}
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name == "" {
				name = f.Name
			}
			out = append(out, name)
		}
	}
	return out
}
