// GENERATED from MACReconciler.Publish's Go AST.
// API/lease summaries are explicit abstractions; requirements are separately authored.
module Publication {
 type Assignment = { machineId: str, epoch: int }
 pure val EMPTY = { machineId: "", epoch: 0 }
 pure val ORIGINAL = { machineId: "host-1", epoch: 1 }
 var accepted: bool
 var claim: Assignment
 var epoch: int
 var err: bool
 var event: str
 var finished: bool
 var lease: Assignment
 var localMachineId: str
 var observed: Assignment
 var pc: int
 var publishedMachineId: str
 var tick: int
 action initialize(initialObservation) = all {
      true,
      accepted' = false,
      claim' = EMPTY,
      epoch' = 1,
      err' = false,
      event' = "init",
      finished' = false,
      lease' = ORIGINAL,
      localMachineId' = "",
      observed' = initialObservation,
      pc' = 12,
      publishedMachineId' = "",
      tick' = 0
    }
 action init = {
 nondet initialObservation = Set(EMPTY, ORIGINAL).oneOf()
 initialize(initialObservation)
 }
 action scenarioInit = initialize(ORIGINAL)

 // Go line 113: return err == nil, err
 action node0(failed) = all {
      not(finished) and pc == 0,
      accepted' = not(err),
      claim' = claim,
      epoch' = epoch,
      err' = err,
      event' = "return",
      finished' = true,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = -1,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }

 // Go line 112: err = r.Client.Status().Update(ctx, infra)
 action node1(failed) = all {
      not(finished) and pc == 1,
      accepted' = accepted,
      claim' = claim,
      epoch' = epoch,
      err' = failed,
      event' = if (failed) "writeError" else "commit",
      finished' = finished,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = 0,
      publishedMachineId' = if (failed) publishedMachineId else localMachineId,
      tick' = tick + 1
    }

 // Go line 110: return false, err
 action node2(failed) = all {
      not(finished) and pc == 2,
      accepted' = false,
      claim' = claim,
      epoch' = epoch,
      err' = err,
      event' = "return",
      finished' = true,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = -1,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }

 // Go line 109: err != nil
 action node3(failed) = all {
      not(finished) and pc == 3,
      accepted' = accepted,
      claim' = claim,
      epoch' = epoch,
      err' = err,
      event' = "branch",
      finished' = finished,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = if (err) 2 else 1,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }

 // Go line 109: err = unstructured.SetNestedField(infra.Object, observed.MachineID, "status", "machineId")
 action node4(failed) = all {
      not(finished) and pc == 4,
      accepted' = accepted,
      claim' = claim,
      epoch' = epoch,
      err' = false,
      event' = "setField",
      finished' = finished,
      lease' = lease,
      localMachineId' = observed.machineId,
      observed' = observed,
      pc' = 3,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }

 // Go line 107: return false, err
 action node5(failed) = all {
      not(finished) and pc == 5,
      accepted' = false,
      claim' = claim,
      epoch' = epoch,
      err' = err,
      event' = "return",
      finished' = true,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = -1,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }

 // Go line 106: err != nil
 action node6(failed) = all {
      not(finished) and pc == 6,
      accepted' = accepted,
      claim' = claim,
      epoch' = epoch,
      err' = err,
      event' = "branch",
      finished' = finished,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = if (err) 5 else 4,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }

 // Go line 105: infra, err := get(ctx, r.Client, infraGVK, key)
 action node7(failed) = all {
      not(finished) and pc == 7,
      accepted' = accepted,
      claim' = claim,
      epoch' = epoch,
      err' = failed,
      event' = "readInfra",
      finished' = finished,
      lease' = lease,
      localMachineId' = if (failed) localMachineId else publishedMachineId,
      observed' = observed,
      pc' = 6,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }

 // Go line 103: return false, nil
 action node8(failed) = all {
      not(finished) and pc == 8,
      accepted' = false,
      claim' = claim,
      epoch' = epoch,
      err' = err,
      event' = "return",
      finished' = true,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = -1,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }

 // Go line 102: observed.MachineID == ""
 action node9(failed) = all {
      not(finished) and pc == 9,
      accepted' = accepted,
      claim' = claim,
      epoch' = epoch,
      err' = err,
      event' = "branch",
      finished' = finished,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = if ((observed.machineId == "")) 8 else 7,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }

 // Go line 100: return false, err
 action node10(failed) = all {
      not(finished) and pc == 10,
      accepted' = false,
      claim' = claim,
      epoch' = epoch,
      err' = err,
      event' = "return",
      finished' = true,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = -1,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }

 // Go line 99: err != nil
 action node11(failed) = all {
      not(finished) and pc == 11,
      accepted' = accepted,
      claim' = claim,
      epoch' = epoch,
      err' = err,
      event' = "branch",
      finished' = finished,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = if (err) 10 else 9,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }

 // Go line 98: claim, err := get(ctx, r.APIReader, claimGVK, key)
 action node12(failed) = all {
      not(finished) and pc == 12,
      accepted' = accepted,
      claim' = if (failed) claim else lease,
      epoch' = epoch,
      err' = failed,
      event' = "readClaim",
      finished' = finished,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = 11,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }
 action program(failed) = any { node0(failed), node1(failed), node2(failed), node3(failed), node4(failed), node5(failed), node6(failed), node7(failed), node8(failed), node9(failed), node10(failed), node11(failed), node12(failed) }
 action revoke = all {
      not(finished) and lease.machineId != "",
      accepted' = accepted,
      claim' = claim,
      epoch' = epoch,
      err' = err,
      event' = "revoke",
      finished' = finished,
      lease' = EMPTY,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = pc,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }
 action grant(host) = all {
      not(finished) and lease.machineId == "" and epoch < 2,
      accepted' = accepted,
      claim' = claim,
      epoch' = epoch + 1,
      err' = err,
      event' = "grant",
      finished' = finished,
      lease' = { machineId: host, epoch: epoch + 1 },
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = pc,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }
 action done = all {
      finished,
      accepted' = accepted,
      claim' = claim,
      epoch' = epoch,
      err' = err,
      event' = "done",
      finished' = finished,
      lease' = lease,
      localMachineId' = localMachineId,
      observed' = observed,
      pc' = pc,
      publishedMachineId' = publishedMachineId,
      tick' = tick + 1
    }
 action step = any { program(false), program(true), revoke, grant("host-1"), grant("host-2"), done }
 action staleStep = if (tick == 0) revoke else any { program(false), done }
 action staleEpochStep = if (tick == 0) revoke else if (tick == 1) grant("host-1") else any { program(false), done }
 action revocationAfterReadStep = if (tick == 1) revoke else any { program(false), done }
 val publicationUsesCheckedLease = ((event != "commit") or (((publishedMachineId != "") and (publishedMachineId == claim.machineId)) and (observed == claim)))
 val publicationUsesLiveLease = ((event != "commit") or (((publishedMachineId != "") and (publishedMachineId == lease.machineId)) and (observed == lease)))
}
