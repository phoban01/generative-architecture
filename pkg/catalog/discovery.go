package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"generative-architecture/pkg/ir"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// LoadDiscovery reads a saved API discovery snapshot: a JSON list of
// metav1.APIResourceList documents, the shape returned by
// `kubectl get --raw /apis/<group>/<version>` or `/api/v1`. Subresources are
// skipped. Entries are marked served; storage is unknown to discovery and
// left false. Origin is "discovery:<path>".
func LoadDiscovery(path string) ([]Entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lists []metav1.APIResourceList
	if err := json.Unmarshal(b, &lists); err != nil {
		// Accept a single list as well as a list of lists.
		var one metav1.APIResourceList
		if err2 := json.Unmarshal(b, &one); err2 != nil || one.GroupVersion == "" {
			return nil, fmt.Errorf("%s: expected a JSON list of APIResourceList: %w", path, err)
		}
		lists = []metav1.APIResourceList{one}
	}
	origin := "discovery:" + path
	var out []Entry
	for _, list := range lists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			group, version := gv.Group, gv.Version
			if r.Group != "" {
				group = r.Group
			}
			if r.Version != "" {
				version = r.Version
			}
			scope := ir.Cluster
			if r.Namespaced {
				scope = ir.Namespaced
			}
			e := Entry{
				GVK:    ir.GVK{Group: group, Version: version, Kind: r.Kind},
				GVR:    ir.GVR{Group: group, Version: version, Resource: r.Name},
				Scope:  scope,
				Served: true,
				Origin: origin,
			}
			if err := e.Validate(); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			out = append(out, e)
		}
	}
	return out, nil
}
