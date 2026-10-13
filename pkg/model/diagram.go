package model

import (
	"fmt"
	"generative-architecture/pkg/effect"
	"strings"
)

// LikeC4 renders declared effect dependencies; it does not claim to discover
// runtime registration or ownership from matching resource shapes. A model-name
// prefix allows independently generated diagrams to share a LikeC4 workspace.
func (m *Compiled) LikeC4() string {
	var b strings.Builder
	prefix := "library_" + m.spec.Name
	fmt.Fprintf(&b, "specification {\n element %sProcess\n element %sResource\n}\nmodel {\n", prefix, prefix)
	process := prefix + "_process"
	fmt.Fprintf(&b, " %s = %sProcess %q\n", process, prefix, m.spec.Name)
	ids := map[effect.Resource]string{}
	include := []string{process}
	for _, e := range m.spec.Effects {
		id, ok := ids[e.Resource]
		if !ok {
			id = fmt.Sprintf("%s_resource%d", prefix, len(ids))
			ids[e.Resource] = id
			include = append(include, id)
			fmt.Fprintf(&b, " %s = %sResource %q { description %q }\n", id, prefix, e.Resource.Kind, e.Resource.Group+"/"+e.Resource.Version)
		}
		fmt.Fprintf(&b, " %s -> %s %q\n", process, id, e.ID+": "+e.Operation+" ("+e.Consistency+")")
	}
	fmt.Fprintf(&b, "}\nviews {\n view %s {\n title %q\n include %s\n }\n}\n", prefix, m.spec.Name, strings.Join(include, ", "))
	return b.String()
}
