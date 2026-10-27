package catalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/ir"

	"k8s.io/apimachinery/pkg/runtime"
)

const metav1Package = "k8s.io/apimachinery/pkg/apis/meta/v1"

// FromScheme lists the kinds registered in s, skipping List kinds, internal
// versions and the metav1 option and status types every group version
// registers. Plurals and scopes are not guessed: every remaining GVK must
// appear in both maps or the call fails naming the first missing one.
// Origin is "scheme".
func FromScheme(s *runtime.Scheme, plurals map[ir.GVK]ir.GVR, scopes map[ir.GVK]ir.Scope) ([]Entry, error) {
	var kinds []ir.GVK
	for gvk, typ := range s.AllKnownTypes() {
		if gvk.Version == runtime.APIVersionInternal || strings.HasSuffix(gvk.Kind, "List") || typ.PkgPath() == metav1Package {
			continue
		}
		kinds = append(kinds, ir.FromSchemaGVK(gvk))
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].String() < kinds[j].String() })
	out := make([]Entry, 0, len(kinds))
	for _, gvk := range kinds {
		gvr, ok := plurals[gvk]
		if !ok {
			return nil, fmt.Errorf("scheme kind %s has no declared plural; supply it from a CRD or discovery snapshot", gvk)
		}
		scope, ok := scopes[gvk]
		if !ok {
			return nil, fmt.Errorf("scheme kind %s has no declared scope", gvk)
		}
		e := Entry{GVK: gvk, GVR: gvr, Scope: scope, Served: true, Origin: "scheme"}
		if err := e.Validate(); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}
