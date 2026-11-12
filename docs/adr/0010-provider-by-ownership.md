# ADR-0010: The provider of a resource is chosen by ownership

Status: accepted (2026-11-11)

## Context

ARCH-10 derives ports from IR facts and joins them by resource. Each derived port
models the objects of one resource in `<kind>_objects`. One component, the provider,
hosts that state. Every other component that touches the resource binds to it.

Until now the effect direction chose the provider. A resource that a component only
writes became an `out` port and provided. A resource that it reads and writes became a
writable `in` port and never provided (#66). Most controllers create an object and then
read it again. So most creators had no provider port, and reference-system joins came
back unresolved (#176).

## Decision

We choose the provider by ownership, not by effect direction.

1. The component whose primary watch (`For`, `ir.Watch.Primary`) is the resource owns
   it and provides it. Every other component that writes the resource, creators
   included, gets a writable `in` port.
2. If no component has the resource as its primary watch, the sole creator provides it.
   A creator is a component with a `create` or `apply` effect on the resource. This
   covers core kinds such as Namespace, Secret and Job.
3. If nobody owns or creates the resource, the join stays unresolved. The diagnostic
   asks for a BoundaryModel that provides the resource or an explicit link.
4. Two primary owners of one resource stay an ambiguous provider (#67). One explicit
   link chooses between them.

A hand-written or boundary `out` port counts as an owner. Ownership is decided per
cluster, contract and version.

`PortsFromIR` marks derived ports with `Owner` and `Creator`. It makes owners and
creators `out` ports. `ResolveProviders` applies rule 1 across components. It turns a
creator into a writable `in` port when another component owns the resource.
`JoinByResource` applies `ResolveProviders` before it joins.

## Rejected alternatives

- A writable `in` port also provides. Nearly every resource has a reader that also
  writes it, for example to set status or add a finalizer. Almost every join would
  become ambiguous and need a hand-written link.
- A create effect yields a separate `out` port. A component that creates objects of a
  CRD it does not reconcile would provide that CRD. The model would place
  `<kind>_objects` in the client, not in the controller that owns the API type.

## Consequences

- Ownership matches the owned API types of ARCH-1 and the owned-resource markers. The
  owner hosts `<kind>_objects`, as the model convention expects.
- The reference system gets a provider for Tenant, Database and Backup from their
  controllers. It gets one for Namespace, ResourceQuota, Secret and Job from their
  creators. It needs no hand-written links.
- A component that only patches or deletes a resource no longer provides it. Without an
  owner or a creator the join is unresolved.
- Callers compose with the components that `ResolveProviders` returns. Otherwise a
  demoted creator is still an `out` port and its link fails the contract check.
