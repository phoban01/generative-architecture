package architecture

import "fmt"

type assignment struct {
	MachineID string
	Epoch     int64
}
type simulatedHost struct {
	Epoch    int64
	Physical string
}
type simulatedInfra struct {
	Wanted   bool
	Call     string
	Claim    bool
	Observed assignment
	Assigned string
	Lease    assignment
}

// Simulator is independently implemented. It never reads authored CEL or expected states.
type Simulator struct {
	hosts    map[string]*simulatedHost
	infras   map[string]*simulatedInfra
	maxEpoch int64
	staleBug bool
}

func NewSimulator(s System, bug bool) *Simulator {
	sim := &Simulator{hosts: map[string]*simulatedHost{}, infras: map[string]*simulatedInfra{}, maxEpoch: s.MaxEpoch, staleBug: bug}
	for _, h := range s.Hosts {
		sim.hosts[h] = &simulatedHost{}
	}
	for _, i := range s.InfraMachines {
		sim.infras[i.ID] = &simulatedInfra{}
	}
	return sim
}
func (s *Simulator) Apply(action string, args map[string]string) (bool, error) {
	i, h := s.infras[args["infra"]], s.hosts[args["host"]]
	if _, ok := args["infra"]; ok && i == nil {
		return false, fmt.Errorf("unknown InfraMachine")
	}
	if _, ok := args["host"]; ok && h == nil {
		return false, fmt.Errorf("unknown host")
	}
	leased := func(host string) bool {
		for _, v := range s.infras {
			if v.Lease.MachineID == host {
				return true
			}
		}
		return false
	}
	bad := func() (bool, error) { return false, fmt.Errorf("precondition not met: %s", action) }
	switch action {
	case "infra.request":
		i.Wanted = true
	case "infra.submit":
		if i.Assigned == "" {
			return bad()
		}
		i.Call = i.Assigned
	case "mac.createClaim":
		if !i.Wanted {
			return bad()
		}
		i.Claim = true
	case "mac.observe":
		if !i.Claim {
			return bad()
		}
		i.Observed = i.Lease
	case "mac.publish":
		if !i.Claim {
			return bad()
		}
		if i.Observed.MachineID == "" {
			return false, nil
		}
		if !s.staleBug && i.Observed != i.Lease {
			return false, nil
		}
		i.Assigned = i.Observed.MachineID
	case "mac.clear":
		if i.Lease.MachineID != "" {
			return bad()
		}
		i.Assigned = ""
	case "claim.grant":
		if !i.Claim || i.Lease.MachineID != "" || h.Physical != "" || h.Epoch >= s.maxEpoch || leased(args["host"]) {
			return bad()
		}
		h.Epoch++
		i.Lease = assignment{args["host"], h.Epoch}
	case "claim.revoke":
		if i.Lease.MachineID == "" {
			return bad()
		}
		i.Lease = assignment{}
	case "service.execute":
		if i.Call == "" {
			return bad()
		}
		machine := s.hosts[i.Call]
		if machine == nil {
			return false, fmt.Errorf("unknown physical machine")
		}
		if machine.Physical != "" {
			return false, nil
		}
		machine.Physical = args["infra"] // No independent lease check, as described.
	case "service.fence":
		if leased(args["host"]) {
			return bad()
		}
		h.Physical = ""
	default:
		return false, fmt.Errorf("unimplemented adapter action %s", action)
	}
	return true, nil
}
func (s *Simulator) Project() (map[string]any, error) {
	out := map[string]any{}
	for _, k := range []string{"infra_wanted", "infra_calls", "mac_claims", "mac_observed", "mac_assigned", "claim_leases", "claim_epochs", "service_machines"} {
		out[k] = map[string]any{}
	}
	set := func(k, id string, v any) { out[k].(map[string]any)[id] = v }
	record := func(a assignment) map[string]any { return map[string]any{"machineId": a.MachineID, "epoch": a.Epoch} }
	for id, i := range s.infras {
		set("infra_wanted", id, i.Wanted)
		set("infra_calls", id, i.Call)
		set("mac_claims", id, i.Claim)
		set("mac_observed", id, record(i.Observed))
		set("mac_assigned", id, i.Assigned)
		set("claim_leases", id, record(i.Lease))
	}
	for id, h := range s.hosts {
		set("claim_epochs", id, h.Epoch)
		set("service_machines", id, h.Physical)
	}
	return out, nil
}
