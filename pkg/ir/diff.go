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
// changed record. PreviousID is the old ID of a changed watch whose ID
// changed with its content.
type Change struct {
	Kind       string   `json:"kind"`
	ID         string   `json:"id"`
	PreviousID string   `json:"previousId,omitempty"`
	Fields     []string `json:"fields,omitempty"`
}

// Delta is the semantic difference between two documents. Records are
// matched by ID. A changed record has the same ID and different content.
// Watches are also paired by controller, cluster, resource and mapping
// kind (see pairWatches), because a watch ID includes the mapping field
// path.
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
	d.pairWatches(old.Watches, new.Watches)
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

// pairWatches reports a removed and an added watch as one changed watch
// when they share controller, cluster, resource and mapping kind, and no
// other removed or added watch shares them. Without the pairing, a changed
// mapping field path shows as one removed and one added watch, since the
// path is part of the watch ID. The change carries the new ID and the old
// one as PreviousID; Fields leaves out the ID.
func (d *Delta) pairWatches(old, new []Watch) {
	slot := func(w Watch) string {
		return stableID(w.Controller, string(w.Cluster), w.Resource.String(), w.Mapping.Kind)
	}
	candidates := func(changes []Change, watches []Watch) map[string][]Watch {
		ids := map[string]bool{}
		for _, c := range changes {
			if c.Kind == RecordWatch {
				ids[c.ID] = true
			}
		}
		out := map[string][]Watch{}
		for _, w := range watches {
			if ids[w.ID] {
				out[slot(w)] = append(out[slot(w)], w)
			}
		}
		return out
	}
	removed, added := candidates(d.Removed, old), candidates(d.Added, new)
	paired := map[string]bool{}
	for s, rs := range removed {
		as := added[s]
		if len(rs) != 1 || len(as) != 1 {
			continue
		}
		var fields []string
		for _, f := range changedFields(rs[0], as[0]) {
			if f != "id" {
				fields = append(fields, f)
			}
		}
		d.Changed = append(d.Changed, Change{Kind: RecordWatch, ID: as[0].ID, PreviousID: rs[0].ID, Fields: fields})
		paired[rs[0].ID], paired[as[0].ID] = true, true
	}
	keep := func(changes []Change) []Change {
		var out []Change
		for _, c := range changes {
			if c.Kind != RecordWatch || !paired[c.ID] {
				out = append(out, c)
			}
		}
		return out
	}
	d.Removed, d.Added = keep(d.Removed), keep(d.Added)
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
