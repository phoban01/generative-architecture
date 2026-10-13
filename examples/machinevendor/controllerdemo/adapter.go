package controllerdemo

import (
	"context"
	"fmt"

	apiv1 "generative-architecture/examples/machinevendor/api/v1alpha1"
	"generative-architecture/examples/machinevendor/architecture"
	"generative-architecture/generated"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type host struct {
	epoch int64
	owner string
}

// Adapter receives actions and arguments only. It never reads CEL or expected
// trace states. Kubernetes objects are the source of truth for claim/publication.
// The surrounding claim controller, InfraMachine provider and machine service
// remain handwritten environment fakes. Only MAC is the controller under test.
type Adapter struct {
	api      client.Client
	mac      *MACReconciler
	system   architecture.System
	observed map[string]Assignment // snapshots held between reconcile phases
	calls    map[string]string     // prepared external calls survive assignment clearing
	hosts    map[string]*host
	ctx      context.Context
}

var _ generated.ActionDriver = (*Adapter)(nil)
var _ architecture.Adapter = (*Adapter)(nil)

func New(s architecture.System, mutation bool) (*Adapter, error) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(infraGVK, &apiv1.InfraMachine{})
	scheme.AddKnownTypeWithName(claimGVK, &apiv1.MachineClaim{})
	objects := []client.Object{}
	for _, i := range s.InfraMachines {
		o := object(infraGVK, key(i.ID))
		o.SetUID(types.UID("demo-" + i.ID))
		o.Object["spec"] = map[string]any{"allocationRequested": false}
		o.Object["status"] = map[string]any{"machineId": ""}
		objects = append(objects, o)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(object(infraGVK, client.ObjectKey{}), object(claimGVK, client.ObjectKey{})).WithObjects(objects...).Build()
	a := &Adapter{api: c, mac: &MACReconciler{Client: c, APIReader: c, AllowStalePublication: mutation}, system: s, observed: map[string]Assignment{}, calls: map[string]string{}, hosts: map[string]*host{}, ctx: context.Background()}
	for _, h := range s.Hosts {
		a.hosts[h] = &host{}
	}
	for _, i := range s.InfraMachines {
		a.observed[i.ID] = Assignment{}
		a.calls[i.ID] = ""
	}
	return a, nil
}
func key(id string) client.ObjectKey { return client.ObjectKey{Namespace: "demo", Name: id} }
func (a *Adapter) Apply(action string, args map[string]string) (bool, error) {
	if id, ok := args["infra"]; ok {
		if _, exists := a.observed[id]; !exists {
			return false, fmt.Errorf("unknown InfraMachine %q", id)
		}
	}
	if id, ok := args["host"]; ok {
		if _, exists := a.hosts[id]; !exists {
			return false, fmt.Errorf("unknown host %q", id)
		}
	}
	return generated.Dispatch(a, action, args)
}
func (a *Adapter) InfraRequest(id string) (bool, error) {
	o, err := get(a.ctx, a.api, infraGVK, key(id))
	if err != nil {
		return false, err
	}
	if err = unstructured.SetNestedField(o.Object, true, "spec", "allocationRequested"); err != nil {
		return false, err
	}
	err = a.api.Update(a.ctx, o)
	return err == nil, err
}
func (a *Adapter) InfraSubmit(id string) (bool, error) {
	o, err := get(a.ctx, a.api, infraGVK, key(id))
	if err != nil {
		return false, err
	}
	machine := str(o, "status", "machineId")
	if machine == "" {
		return false, fmt.Errorf("no published machine ID")
	}
	a.calls[id] = machine
	return true, nil
}
func (a *Adapter) MacCreateClaim(id string) (bool, error) { return a.mac.EnsureClaim(a.ctx, key(id)) }
func (a *Adapter) MacObserve(id string) (bool, error) {
	v, err := a.mac.Observe(a.ctx, key(id))
	if err != nil {
		return false, err
	}
	a.observed[id] = v
	return true, nil
}
func (a *Adapter) MacPublish(id string) (bool, error) {
	return a.mac.Publish(a.ctx, key(id), a.observed[id])
}
func (a *Adapter) MacClear(id string) (bool, error) { return a.mac.Clear(a.ctx, key(id)) }
func (a *Adapter) leases() (map[string]Assignment, error) {
	out := map[string]Assignment{}
	for _, i := range a.system.InfraMachines {
		claim, err := get(a.ctx, a.api, claimGVK, key(i.ID))
		if apierrors.IsNotFound(err) {
			out[i.ID] = Assignment{}
			continue
		}
		if err != nil {
			return nil, err
		}
		out[i.ID] = assignmentOf(claim)
	}
	return out, nil
}
func (a *Adapter) ClaimGrant(id, h string) (bool, error) {
	claim, err := get(a.ctx, a.api, claimGVK, key(id))
	if err != nil {
		return false, err
	}
	leases, err := a.leases()
	if err != nil {
		return false, err
	}
	machine := a.hosts[h]
	if leases[id].MachineID != "" || machine.owner != "" || machine.epoch >= a.system.MaxEpoch {
		return false, fmt.Errorf("host/claim unavailable")
	}
	for _, lease := range leases {
		if lease.MachineID == h {
			return false, fmt.Errorf("host already leased")
		}
	}
	next := machine.epoch + 1
	setAssignment(claim, Assignment{MachineID: h, Epoch: next})
	if err = a.api.Status().Update(a.ctx, claim); err != nil {
		return false, err
	}
	machine.epoch = next
	return true, nil
}
func (a *Adapter) ClaimRevoke(id string) (bool, error) {
	claim, err := get(a.ctx, a.api, claimGVK, key(id))
	if err != nil {
		return false, err
	}
	if assignmentOf(claim).MachineID == "" {
		return false, fmt.Errorf("claim has no lease")
	}
	setAssignment(claim, Assignment{})
	err = a.api.Status().Update(a.ctx, claim)
	return err == nil, err
}
func (a *Adapter) ServiceExecute(id string) (bool, error) {
	machine := a.hosts[a.calls[id]]
	if machine == nil {
		return false, fmt.Errorf("no prepared provisioning call")
	}
	if machine.owner != "" {
		return false, nil
	}
	machine.owner = id // Intentionally no independent lease authorization check.
	return true, nil
}
func (a *Adapter) ServiceFence(h string) (bool, error) {
	leases, err := a.leases()
	if err != nil {
		return false, err
	}
	for _, lease := range leases {
		if lease.MachineID == h {
			return false, fmt.Errorf("host still leased")
		}
	}
	a.hosts[h].owner = ""
	return true, nil
}
func record(v Assignment) map[string]any {
	return map[string]any{"machineId": v.MachineID, "epoch": v.Epoch}
}
func (a *Adapter) Project() (map[string]any, error) {
	out := map[string]any{}
	for _, field := range []string{"infra_wanted", "infra_calls", "mac_claims", "mac_observed", "mac_assigned", "claim_leases", "claim_epochs", "service_machines"} {
		out[field] = map[string]any{}
	}
	set := func(field, id string, value any) { out[field].(map[string]any)[id] = value }
	leases, err := a.leases()
	if err != nil {
		return nil, err
	}
	for _, i := range a.system.InfraMachines {
		o, err := get(a.ctx, a.api, infraGVK, key(i.ID))
		if err != nil {
			return nil, err
		}
		_, err = get(a.ctx, a.api, claimGVK, key(i.ID))
		exists := err == nil
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, err
		}
		set("infra_wanted", i.ID, wanted(o))
		set("infra_calls", i.ID, a.calls[i.ID])
		set("mac_claims", i.ID, exists)
		set("mac_observed", i.ID, record(a.observed[i.ID]))
		set("mac_assigned", i.ID, str(o, "status", "machineId"))
		set("claim_leases", i.ID, record(leases[i.ID]))
	}
	for id, h := range a.hosts {
		set("claim_epochs", id, h.epoch)
		set("service_machines", id, h.owner)
	}
	return out, nil
}
