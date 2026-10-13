// GENERATED from YAML and CEL by the Go architecture compiler.
module System {
  type Assignment = { machineId: str, epoch: int }
  pure val EMPTY_ASSIGNMENT = { machineId: "", epoch: 0 }
  pure val HOSTS = Set("host-1", "host-2")
  pure val INFRAS = Set("infra-reserve", "infra-customer")
  pure val MAX_EPOCH = 3
  var infra_calls: str -> str
  var infra_wanted: str -> bool
  var mac_assigned: str -> str
  var mac_claims: str -> bool
  var mac_observed: str -> Assignment
  var claim_epochs: str -> int
  var claim_leases: str -> Assignment
  var service_machines: str -> str
  var tick: int
  var event: { name: str, host: str, infra: str, accepted: bool }

  action init = all {
    infra_calls' = INFRAS.mapBy(_ => ""),
    infra_wanted' = INFRAS.mapBy(_ => false),
    mac_assigned' = INFRAS.mapBy(_ => ""),
    mac_claims' = INFRAS.mapBy(_ => false),
    mac_observed' = INFRAS.mapBy(_ => EMPTY_ASSIGNMENT),
    claim_epochs' = HOSTS.mapBy(_ => 0),
    claim_leases' = INFRAS.mapBy(_ => EMPTY_ASSIGNMENT),
    service_machines' = HOSTS.mapBy(_ => ""),
    tick' = 0,
    event' = { name: "init", host: "host-1", infra: "infra-reserve", accepted: true }
  }

  action infra_request(infra) = {
    all {
      true,
      infra_calls' = infra_calls,
      infra_wanted' = infra_wanted.set(infra, true),
      mac_assigned' = mac_assigned,
      mac_claims' = mac_claims,
      mac_observed' = mac_observed,
      claim_epochs' = claim_epochs,
      claim_leases' = claim_leases,
      service_machines' = service_machines,
      tick' = tick + 1,
      event' = { name: "infra.request", host: "host-1", infra: infra, accepted: true }
    }
  }

  action infra_submit(infra) = {
    all {
      (mac_assigned.get(infra) != ""),
      infra_calls' = infra_calls.set(infra, mac_assigned.get(infra)),
      infra_wanted' = infra_wanted,
      mac_assigned' = mac_assigned,
      mac_claims' = mac_claims,
      mac_observed' = mac_observed,
      claim_epochs' = claim_epochs,
      claim_leases' = claim_leases,
      service_machines' = service_machines,
      tick' = tick + 1,
      event' = { name: "infra.submit", host: "host-1", infra: infra, accepted: true }
    }
  }

  action mac_createClaim(infra) = {
    all {
      infra_wanted.get(infra),
      infra_calls' = infra_calls,
      infra_wanted' = infra_wanted,
      mac_assigned' = mac_assigned,
      mac_claims' = mac_claims.set(infra, true),
      mac_observed' = mac_observed,
      claim_epochs' = claim_epochs,
      claim_leases' = claim_leases,
      service_machines' = service_machines,
      tick' = tick + 1,
      event' = { name: "mac.createClaim", host: "host-1", infra: infra, accepted: true }
    }
  }

  action mac_observe(infra) = {
    all {
      mac_claims.get(infra),
      infra_calls' = infra_calls,
      infra_wanted' = infra_wanted,
      mac_assigned' = mac_assigned,
      mac_claims' = mac_claims,
      mac_observed' = mac_observed.set(infra, claim_leases.get(infra)),
      claim_epochs' = claim_epochs,
      claim_leases' = claim_leases,
      service_machines' = service_machines,
      tick' = tick + 1,
      event' = { name: "mac.observe", host: "host-1", infra: infra, accepted: true }
    }
  }

  action mac_publish(infra) = {
    val allowed = ((mac_observed.get(infra).machineId != "") and (mac_observed.get(infra) == claim_leases.get(infra)))
    all {
      mac_claims.get(infra),
      infra_calls' = infra_calls,
      infra_wanted' = infra_wanted,
      mac_assigned' = if (allowed) mac_assigned.set(infra, mac_observed.get(infra).machineId) else mac_assigned,
      mac_claims' = mac_claims,
      mac_observed' = mac_observed,
      claim_epochs' = claim_epochs,
      claim_leases' = claim_leases,
      service_machines' = service_machines,
      tick' = tick + 1,
      event' = { name: "mac.publish", host: "host-1", infra: infra, accepted: allowed }
    }
  }

  action mac_clear(infra) = {
    all {
      (claim_leases.get(infra).machineId == ""),
      infra_calls' = infra_calls,
      infra_wanted' = infra_wanted,
      mac_assigned' = mac_assigned.set(infra, ""),
      mac_claims' = mac_claims,
      mac_observed' = mac_observed,
      claim_epochs' = claim_epochs,
      claim_leases' = claim_leases,
      service_machines' = service_machines,
      tick' = tick + 1,
      event' = { name: "mac.clear", host: "host-1", infra: infra, accepted: true }
    }
  }

  action claim_grant(infra, host) = {
    all {
      (((mac_claims.get(infra) and (claim_leases.get(infra).machineId == "")) and INFRAS.forall(i => (claim_leases.get(i).machineId != host))) and ((service_machines.get(host) == "") and (claim_epochs.get(host) < MAX_EPOCH))),
      infra_calls' = infra_calls,
      infra_wanted' = infra_wanted,
      mac_assigned' = mac_assigned,
      mac_claims' = mac_claims,
      mac_observed' = mac_observed,
      claim_epochs' = claim_epochs.set(host, (claim_epochs.get(host) + 1)),
      claim_leases' = claim_leases.set(infra, { epoch: (claim_epochs.get(host) + 1), machineId: host }),
      service_machines' = service_machines,
      tick' = tick + 1,
      event' = { name: "claim.grant", host: host, infra: infra, accepted: true }
    }
  }

  action claim_revoke(infra) = {
    all {
      (claim_leases.get(infra).machineId != ""),
      infra_calls' = infra_calls,
      infra_wanted' = infra_wanted,
      mac_assigned' = mac_assigned,
      mac_claims' = mac_claims,
      mac_observed' = mac_observed,
      claim_epochs' = claim_epochs,
      claim_leases' = claim_leases.set(infra, EMPTY_ASSIGNMENT),
      service_machines' = service_machines,
      tick' = tick + 1,
      event' = { name: "claim.revoke", host: "host-1", infra: infra, accepted: true }
    }
  }

  action service_execute(infra) = {
    val machineId = infra_calls.get(infra)
    val allowed = (service_machines.get(machineId) == "")
    all {
      (infra_calls.get(infra) != ""),
      infra_calls' = infra_calls,
      infra_wanted' = infra_wanted,
      mac_assigned' = mac_assigned,
      mac_claims' = mac_claims,
      mac_observed' = mac_observed,
      claim_epochs' = claim_epochs,
      claim_leases' = claim_leases,
      service_machines' = if (allowed) service_machines.set(infra_calls.get(infra), infra) else service_machines,
      tick' = tick + 1,
      event' = { name: "service.execute", host: "host-1", infra: infra, accepted: allowed }
    }
  }

  action service_fence(host) = {
    all {
      INFRAS.forall(i => (claim_leases.get(i).machineId != host)),
      infra_calls' = infra_calls,
      infra_wanted' = infra_wanted,
      mac_assigned' = mac_assigned,
      mac_claims' = mac_claims,
      mac_observed' = mac_observed,
      claim_epochs' = claim_epochs,
      claim_leases' = claim_leases,
      service_machines' = service_machines.set(host, ""),
      tick' = tick + 1,
      event' = { name: "service.fence", host: host, infra: "infra-reserve", accepted: true }
    }
  }

  action halt = all { false,
      infra_calls' = infra_calls,
      infra_wanted' = infra_wanted,
      mac_assigned' = mac_assigned,
      mac_claims' = mac_claims,
      mac_observed' = mac_observed,
      claim_epochs' = claim_epochs,
      claim_leases' = claim_leases,
      service_machines' = service_machines,
    tick' = tick, event' = event
  }

  action step = {
    nondet host = HOSTS.oneOf()
    nondet infra = INFRAS.oneOf()
    any {
      infra_request(infra),
      infra_submit(infra),
      mac_createClaim(infra),
      mac_observe(infra),
      mac_publish(infra),
      mac_clear(infra),
      claim_grant(infra, host),
      claim_revoke(infra),
      service_execute(infra),
      service_fence(host)
    }
  }
  val publicationRequiresCurrentLease = (((event.name != "mac.publish") or not(event.accepted)) or ((mac_assigned.get(event.infra) == claim_leases.get(event.infra).machineId) and (claim_leases.get(event.infra).machineId != "")))
  val exclusiveLeases = INFRAS.forall(a => INFRAS.forall(b => (((a == b) or (claim_leases.get(a).machineId == "")) or (claim_leases.get(a).machineId != claim_leases.get(b).machineId))))
  val provisionRequiresCurrentLease = (((event.name != "service.execute") or not(event.accepted)) or (infra_calls.get(event.infra) == claim_leases.get(event.infra).machineId))

  action revokedAfterPublicationStep =
    if (tick == 0) infra_request("infra-reserve") else
    if (tick == 1) mac_createClaim("infra-reserve") else
    if (tick == 2) claim_grant("infra-reserve", "host-1") else
    if (tick == 3) mac_observe("infra-reserve") else
    if (tick == 4) mac_publish("infra-reserve") else
    if (tick == 5) infra_submit("infra-reserve") else
    if (tick == 6) claim_revoke("infra-reserve") else
    if (tick == 7) mac_clear("infra-reserve") else
    if (tick == 8) service_execute("infra-reserve") else
    halt

  action staleEpochStep =
    if (tick == 0) infra_request("infra-reserve") else
    if (tick == 1) mac_createClaim("infra-reserve") else
    if (tick == 2) claim_grant("infra-reserve", "host-1") else
    if (tick == 3) mac_observe("infra-reserve") else
    if (tick == 4) claim_revoke("infra-reserve") else
    if (tick == 5) claim_grant("infra-reserve", "host-1") else
    if (tick == 6) mac_publish("infra-reserve") else
    halt

  action staleLeaseStep =
    if (tick == 0) infra_request("infra-reserve") else
    if (tick == 1) mac_createClaim("infra-reserve") else
    if (tick == 2) claim_grant("infra-reserve", "host-1") else
    if (tick == 3) mac_observe("infra-reserve") else
    if (tick == 4) claim_revoke("infra-reserve") else
    if (tick == 5) mac_publish("infra-reserve") else
    halt

  action transferFromReserveStep =
    if (tick == 0) infra_request("infra-reserve") else
    if (tick == 1) mac_createClaim("infra-reserve") else
    if (tick == 2) claim_grant("infra-reserve", "host-1") else
    if (tick == 3) mac_observe("infra-reserve") else
    if (tick == 4) mac_publish("infra-reserve") else
    if (tick == 5) infra_submit("infra-reserve") else
    if (tick == 6) service_execute("infra-reserve") else
    if (tick == 7) claim_revoke("infra-reserve") else
    if (tick == 8) mac_clear("infra-reserve") else
    if (tick == 9) service_fence("host-1") else
    if (tick == 10) infra_request("infra-customer") else
    if (tick == 11) mac_createClaim("infra-customer") else
    if (tick == 12) claim_grant("infra-customer", "host-1") else
    if (tick == 13) mac_observe("infra-customer") else
    if (tick == 14) mac_publish("infra-customer") else
    if (tick == 15) infra_submit("infra-customer") else
    if (tick == 16) service_execute("infra-customer") else
    halt
}
