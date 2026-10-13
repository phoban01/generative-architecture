package runtimecheck

import (
	"fmt"
	"generative-architecture/examples/machinevendor/architecture"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func writeCompositionDiagram(root string, trace architecture.Trace) error {
	var b strings.Builder
	b.WriteString(`// Generated from a Quint counterexample successfully replayed against paused Go.
model { runtimeEnvironment = runtimePart "Controlled environment" { description "Schedules lease changes, API faults and cache delivery in the test harness." } }
views { dynamic view composed_publication_race {
 variant sequence
 title 'Replayed Go publication: revocation between read and write'
 description 'Actual unmodified MAC.Publish, real worker/queue, controlled fake API and cache; no production claim.'
`)
	for _, state := range trace.States[1:] {
		var from, to, label string
		switch state["event"] {
		case "enqueue":
			from, to, label = "runtimeEnvironment", "runtimeQueue", "enqueue key"
		case "start":
			from, to, label = "runtimeQueue", "runtimeWorker", "begin reconcile"
		case "observe":
			from, to, label = "runtimeProcess", "runtimeCache", "observe cached assignment"
		case "begin":
			from, to, label = "runtimeWorker", "runtimeProcess", "invoke Publish with retained observation"
		case "readClaim":
			from, to, label = "runtimeProcess", "runtimeAPI", "read lease and retain snapshot"
		case "revoke":
			from, to, label = "runtimeEnvironment", "runtimeAPI", "revoke lease"
		case "cache-sync":
			from, to, label = "runtimeAPI", "runtimeCache", "deliver revoked assignment"
		case "readInfra":
			from, to, label = "runtimeProcess", "runtimeCache", "read InfraMachine and its resourceVersion"
		case "commit":
			from, to, label = "runtimeProcess", "runtimeAPI", "commit machine ID from retained snapshot"
		default:
			continue
		}
		fmt.Fprintf(&b, " %s -> %s %s\n", from, to, strconv.Quote(label))
	}
	b.WriteString("} }\n")
	return os.WriteFile(filepath.Join(root, "generated/composition-race.c4"), []byte(b.String()), 0644)
}
