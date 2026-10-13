package behaviorgen

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"generative-architecture/examples/machinevendor/architecture"
)

func hash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

var variables = map[string]string{
	"pc": "int", "lease": "Assignment", "claim": "Assignment", "observed": "Assignment",
	"localMachineId": "str", "publishedMachineId": "str", "err": "bool", "accepted": "bool",
	"finished": "bool", "event": "str", "tick": "int", "epoch": "int",
}

func sortedKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func transition(guard string, updates map[string]string) string {
	var parts []string
	parts = append(parts, guard)
	for _, name := range sortedKeys(variables) {
		value, ok := updates[name]
		if !ok {
			value = name
		}
		parts = append(parts, name+"' = "+value)
	}
	return "all {\n      " + strings.Join(parts, ",\n      ") + "\n    }"
}
func (p *Program) Quint() (string, error) {
	var b strings.Builder
	fmt.Fprintln(&b, "// GENERATED from MACReconciler.Publish's Go AST.\n// API/lease summaries are explicit abstractions; requirements are separately authored.\nmodule Publication {\n type Assignment = { machineId: str, epoch: int }\n pure val EMPTY = { machineId: \"\", epoch: 0 }\n pure val ORIGINAL = { machineId: \"host-1\", epoch: 1 }")
	for _, name := range sortedKeys(variables) {
		fmt.Fprintf(&b, " var %s: %s\n", name, variables[name])
	}
	init := map[string]string{"pc": strconv.Itoa(p.Entry), "lease": "ORIGINAL", "observed": "initialObservation", "claim": "EMPTY", "localMachineId": `""`, "publishedMachineId": `""`, "err": "false", "accepted": "false", "finished": "false", "event": `"init"`, "tick": "0", "epoch": "1"}
	fmt.Fprintf(&b, " action initialize(initialObservation) = %s\n action init = {\n nondet initialObservation = Set(EMPTY, ORIGINAL).oneOf()\n initialize(initialObservation)\n }\n action scenarioInit = initialize(ORIGINAL)\n", transition("true", init))
	for _, n := range p.Nodes {
		updates := map[string]string{"pc": strconv.Itoa(n.Next), "event": strconv.Quote(n.Kind), "tick": "tick + 1"}
		switch n.Kind {
		case "branch":
			updates["pc"] = fmt.Sprintf("if (%s) %d else %d", n.Expression, n.Next, n.Else)
		case "readClaim":
			updates["claim"] = "if (failed) claim else lease"
			updates["err"] = "failed"
		case "readInfra":
			updates["localMachineId"] = "if (failed) localMachineId else publishedMachineId"
			updates["err"] = "failed"
		case "setField":
			updates["localMachineId"] = n.Expression
			updates["err"] = "false"
		case "updateStatus":
			updates["publishedMachineId"] = "if (failed) publishedMachineId else localMachineId"
			updates["err"] = "failed"
			updates["event"] = `if (failed) "writeError" else "commit"`
		case "return":
			updates["accepted"] = n.Expression
			updates["finished"] = "true"
		default:
			return "", fmt.Errorf("unknown extracted operation %s", n.Kind)
		}
		fmt.Fprintf(&b, "\n // Go line %d: %s\n action node%d(failed) = %s\n", n.Line, strings.ReplaceAll(n.Source, "\n", " "), n.ID, transition(fmt.Sprintf("not(finished) and pc == %d", n.ID), updates))
	}
	var calls []string
	for _, n := range p.Nodes {
		calls = append(calls, fmt.Sprintf("node%d(failed)", n.ID))
	}
	fmt.Fprintf(&b, " action program(failed) = any { %s }\n", strings.Join(calls, ", "))
	fmt.Fprintf(&b, " action revoke = %s\n", transition(`not(finished) and lease.machineId != ""`, map[string]string{"lease": "EMPTY", "tick": "tick + 1", "event": `"revoke"`}))
	fmt.Fprintf(&b, " action grant(host) = %s\n", transition(`not(finished) and lease.machineId == "" and epoch < 2`, map[string]string{"lease": "{ machineId: host, epoch: epoch + 1 }", "epoch": "epoch + 1", "tick": "tick + 1", "event": `"grant"`}))
	fmt.Fprintf(&b, " action done = %s\n", transition("finished", map[string]string{"tick": "tick + 1", "event": `"done"`}))
	fmt.Fprintln(&b, ` action step = any { program(false), program(true), revoke, grant("host-1"), grant("host-2"), done }
 action staleStep = if (tick == 0) revoke else any { program(false), done }
 action staleEpochStep = if (tick == 0) revoke else if (tick == 1) grant("host-1") else any { program(false), done }
 action revocationAfterReadStep = if (tick == 1) revoke else any { program(false), done }`)
	requirements, err := p.QuintRequirements()
	if err != nil {
		return "", err
	}
	b.WriteString(requirements)
	fmt.Fprintln(&b, "}")
	return b.String(), nil
}

// QuintRequirements translates the independently authored properties for reuse in compositions.
func (p *Program) QuintRequirements() (string, error) {
	var b strings.Builder
	names := map[string]bool{}
	for _, pr := range p.cfg.Requirements {
		if pr.Name != "publicationUsesCheckedLease" && pr.Name != "publicationUsesLiveLease" {
			return "", fmt.Errorf("unsupported requirement name %s", pr.Name)
		}
		if names[pr.Name] {
			return "", fmt.Errorf("duplicate requirement")
		}
		names[pr.Name] = true
		expr, err := architecture.CompileCEL(pr.Expression, scope(), "bool")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, " val %s = %s\n", pr.Name, expr.Quint)
	}
	if len(names) != 2 {
		return "", fmt.Errorf("demo requires both separately authored properties")
	}
	return b.String(), nil
}

func (p *Program) Selection() Config { return p.cfg }
