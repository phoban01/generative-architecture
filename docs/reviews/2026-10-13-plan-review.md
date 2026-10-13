# Feedback on the generative architecture project plan

Reviewed: 2026-10-13  
Plan: `PROJECT-PLAN.md`, draft 2 (2026-10-13)  
Basis: review of the plan and targeted inspection of the current prototype; no new verification runs were performed for this review.

## Overall assessment

Draft 2 is a stronger implementation plan. The shared intermediate representation (IR), explicit provenance, independently authored constraints, fail-closed extraction and removal of bespoke integration code all support the intended product.

The objective remains an automatically generated model of an existing system, against which users can express constraints and check implementation conformance. The main concerns are an under-specified state-abstraction step and a delivery sequence that postpones the most important adoption risks.

The suggestions below are changes to the plan and implementation sequence. They do not require a new acceptance-criteria exercise.

## 1. Make state abstraction an explicit workstream

Relevant requirements: ARCH-6, ARCH-7, ARCH-8, ADOPT-3.

Extracting watches, API calls and control flow does not by itself establish how Kubernetes objects become a manageable executable model. The plan needs an explicit treatment of:

- Which object fields and local values enter the model, and how those choices follow from source and properties.
- How object populations, strings, collections and other values are bounded.
- How references and identity are preserved across resources and clusters.
- How real API state is projected into the same abstract state during replay.
- Which behaviours an abstraction preserves, excludes or deliberately over-approximates.

The assignment example currently supplies much of this manually, including model state and the mapping from API objects to that state. Moving that mapping into generated code is central to automatic generation.

Suggested change: give abstraction and projection a named responsibility in the architecture and an early implementation task. Keep user-selected bounds and assumptions explicit in the IR and reports. A bounded passing result must retain those qualifications.

## 2. Prove the complete pipeline earlier

Relevant milestones: M1, M2, M5 and M7.

Generated adapters arrive in M5, while adoption on an external controller arrives in M7. These are major feasibility risks: discovering architecture is easier than running an existing controller under generated instrumentation without bespoke integration code.

After a minimal IR and catalog, take one small existing controller through the complete pipeline:

```text
Existing source
  -> generated behaviour, effects and watches
  -> bounded executable model
  -> Quint exploration
  -> generated integration for replay against Go
  -> conformance result
```

Include dynamic-client access and a deliberately broken implementation that conformance detects. Establish the supported construction and dependency-injection patterns explicitly; do not hide project-specific setup in a new library helper.

The neutral reference system is useful, but it should be accompanied early by an existing controller that was not written to suit the extractor. Keep domain examples as regression fixtures without introducing domain logic into the generic library.

Suggested change: pull a narrow part of adapter generation and external adoption forward. Expand the client surface, upstream boundaries and diagram views after this first complete flow works.

## 3. Separate connectivity contracts from behavioural contracts

Relevant requirement: CON-4; related work: ARCH-10 and M4.

CON-4 currently describes behavioural contracts as versioned ports and marks them complete. Matching a consumer to a compatible provider establishes connectivity. It does not establish the protocol between them.

For MAC, the behaviour includes recognizing a compatible InfraMachine, creating or reusing a MachineClaim, waiting for fulfillment and publishing the corresponding machine ID. Those ordering and state relationships are distinct from selecting which component provides a port.

Suggested change: describe the existing capability as versioned port compatibility and binding. Track behavioural protocol semantics and their conformance separately, including preconditions, permitted transitions and independently authored properties. Do not treat successful auto-linking as evidence of behavioural correctness.

## 4. Keep external effects within the modelling scope

Relevant sections: product definition, effect vocabulary and EXT-1 boundary models.

The definition says projects interact only through the Kubernetes API. Taken literally, this excludes the external machine-provisioning call from the original system. That call matters when checking whether provisioning can proceed using a stale assignment.

Kubernetes can remain the primary integration mechanism while external calls are represented as explicit effects with declared boundary semantics. Supporting those boundaries does not require extracting arbitrary SDK implementations.

Suggested change: clarify that the initial extraction focus is Kubernetes-mediated coordination, while external operations remain visible as effects. Preserve assumptions, outcomes and relevant ordering at these boundaries, including when their internals are not extracted.

## 5. Tighten evidence and execution requirements

Relevant requirements: EVID-1, EXT-5, ADOPT-7 and ADOPT-8.

EVID-1 claims immutable receipts that are invalidated when any input changes. The inspected implementation hashes selected input sets and checks drift; that is useful, but does not establish immutable evidence covering the entire input closure. Mark the broader claim partial until it is implemented and checked.

Additional clarifications:

- Accept supported tool-version ranges for adoption, while recording the exact resolved versions in every execution receipt. Compatibility and reproducibility are separate concerns.
- Allow envtest to skip when assets are absent locally. A required CI integration job should install the assets and fail if it cannot execute, rather than silently report success through a skip.
- Preserve the independence of actual-state observation during generated replay. The adapter must read implementation state; it must not manufacture observations from the expected model state.
- Ensure the receipt inventory covers the inputs actually consumed, including relevant source dependencies, schemas, configuration, boundary assumptions and tool versions.

## 6. Use small issues without assuming trivial integration

Relevant section: work breakdown.

Small implementation issues are useful, but “a few minutes” is not a reliable sizing assumption for extraction, composition or replay semantics. Package-level tests can pass while the assembled pipeline models the wrong behaviour.

Suggested change: retain small issues and explicit dependencies, with integration checkpoints that exercise the complete pipeline. Treat the full issue list as a roadmap rather than making the entire surface a prerequisite for learning whether adoption works.

## Recommended first delivery

Deliver one existing controller with generated behaviour, generated watch and effect integration, generated replay support, and a detected behavioural regression. Use this result to validate the abstraction and adapter interfaces before expanding the implementation surface.

The review did not modify `PROJECT-PLAN.md` or any GitHub issues.
