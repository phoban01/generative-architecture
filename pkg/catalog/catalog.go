// Package catalog maps API kinds to resources and scopes. Entries come from
// CRD manifests, discovery snapshots or schemes with explicit plurals; the
// catalog never guesses a plural.
package catalog

import (
	"fmt"
	"sort"

	"generative-architecture/pkg/ir"

	"k8s.io/apimachinery/pkg/api/meta"
)

// Entry is one served kind.
type Entry struct {
	GVK     ir.GVK   `yaml:"gvk"`
	GVR     ir.GVR   `yaml:"gvr"`
	Scope   ir.Scope `yaml:"scope"`
	Served  bool     `yaml:"served"`
	Storage bool     `yaml:"storage"`
	// Origin says where the entry came from (a CRD file, a discovery
	// snapshot, a scheme registration) for receipts.
	Origin string `yaml:"origin,omitempty"`
}

// Validate requires a complete GVK and GVR in the same group and version and
// a known scope.
func (e Entry) Validate() error {
	if err := e.GVK.Validate(); err != nil {
		return err
	}
	if err := e.GVR.Validate(); err != nil {
		return err
	}
	if e.GVK.Group != e.GVR.Group || e.GVK.Version != e.GVR.Version {
		return fmt.Errorf("entry %s: resource %s is in a different group or version", e.GVK, e.GVR)
	}
	if e.Scope != ir.Namespaced && e.Scope != ir.Cluster {
		return fmt.Errorf("entry %s: scope must be Namespaced or Cluster, got %q", e.GVK, e.Scope)
	}
	return nil
}

// Catalog is a consistent set of entries.
type Catalog struct {
	entries map[ir.GVK]Entry
	byGVR   map[ir.GVR]ir.GVK
}

// New returns an empty catalog.
func New() *Catalog {
	return &Catalog{entries: map[ir.GVK]Entry{}, byGVR: map[ir.GVR]ir.GVK{}}
}

// Add inserts an entry. It fails when the same GVR already maps to a
// different GVK, or the same GVK already maps to a different GVR or scope.
// Re-adding an identical mapping is allowed and keeps the first origin.
func (c *Catalog) Add(e Entry) error {
	if c.entries == nil {
		c.entries = map[ir.GVK]Entry{}
		c.byGVR = map[ir.GVR]ir.GVK{}
	}
	if err := e.Validate(); err != nil {
		return err
	}
	if existing, ok := c.entries[e.GVK]; ok {
		if existing.GVR != e.GVR || existing.Scope != e.Scope {
			return fmt.Errorf("catalog conflict for %s: %s %s (from %s) versus %s %s (from %s)", e.GVK, existing.GVR, existing.Scope, existing.Origin, e.GVR, e.Scope, e.Origin)
		}
		return nil
	}
	if gvk, ok := c.byGVR[e.GVR]; ok && gvk != e.GVK {
		return fmt.Errorf("catalog conflict for %s: already maps to %s (from %s), cannot also map to %s (from %s)", e.GVR, gvk, c.entries[gvk].Origin, e.GVK, e.Origin)
	}
	c.entries[e.GVK] = e
	c.byGVR[e.GVR] = e.GVK
	return nil
}

// Lookup returns the entry for a kind.
func (c *Catalog) Lookup(gvk ir.GVK) (Entry, bool) {
	e, ok := c.entries[gvk]
	return e, ok
}

// ByGVR returns the entry for a resource.
func (c *Catalog) ByGVR(gvr ir.GVR) (Entry, bool) {
	gvk, ok := c.byGVR[gvr]
	if !ok {
		return Entry{}, false
	}
	return c.entries[gvk], true
}

// Entries returns every entry sorted by GVK string.
func (c *Catalog) Entries() []Entry {
	out := make([]Entry, 0, len(c.entries))
	for _, e := range c.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GVK.String() < out[j].GVK.String() })
	return out
}

// RESTMapper builds a mapper that resolves exactly the catalogued kinds. The
// singular resource name is not guessed: the plural is registered for both.
func (c *Catalog) RESTMapper() meta.RESTMapper {
	m := meta.NewDefaultRESTMapper(nil)
	for _, e := range c.Entries() {
		scope := meta.RESTScopeNamespace
		if e.Scope == ir.Cluster {
			scope = meta.RESTScopeRoot
		}
		m.AddSpecific(e.GVK.Schema(), e.GVR.Schema(), e.GVR.Schema(), scope)
	}
	return m
}
