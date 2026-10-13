package sourcegen

import (
	"fmt"
	"strconv"
	"strings"
)

// LikeC4 shows declared implementation mappings separately from behavioral wiring.
func (d Descriptor) LikeC4() string {
	var b strings.Builder
	q := strconv.Quote
	fmt.Fprintln(&b, "// GENERATED from Go types and declared +arch markers.\nspecification {\n element sourceController\n element sourceResource\n}\nmodel {")
	ids := map[string]string{}
	var include []string
	for i, r := range d.Resources {
		id := fmt.Sprintf("sourceResource%d", i)
		ids[r.ID] = id
		include = append(include, id)
		fmt.Fprintf(&b, " %s = sourceResource %s { description %s }\n", id, q(r.Kind), q(r.Group+"/"+r.Version+"; "+r.ID+"; "+r.Source.File))
	}
	for i, c := range d.Controllers {
		id := fmt.Sprintf("sourceController%d", i)
		include = append(include, id)
		fmt.Fprintf(&b, " %s = sourceController %s { description %s }\n", id, q(c.Component+" controller"), q(c.GoType+"; declared primary "+c.Primary+"; declared owns "+c.Owns+"; runtime registration not inferred"))
		for _, p := range c.Ports {
			from, to := id, ids[p.Resource]
			if p.Direction == "in" {
				from, to = to, from
			}
			fmt.Fprintf(&b, " %s -> %s %s { description %s }\n", from, to, q(p.Name+" / "+p.JSONPath), q(p.Type+"; "+p.Protocol+"; "+p.Evidence))
		}
	}
	fmt.Fprintf(&b, "}\nviews {\n view code_structure {\n title 'Go types and declared controller mappings'\n include %s\n autoLayout TopBottom\n }\n}\n", strings.Join(include, ", "))
	return b.String()
}
