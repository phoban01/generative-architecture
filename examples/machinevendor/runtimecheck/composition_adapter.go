package runtimecheck

import (
	"context"
	"fmt"
	apiv1 "generative-architecture/examples/machinevendor/api/v1alpha1"
	"generative-architecture/examples/machinevendor/architecture"
	"generative-architecture/examples/machinevendor/controllerdemo"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/effect"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"reflect"
)

type boundaryResult struct {
	err     error
	machine string
	epoch   int64
	version string
}
type boundary struct {
	kind             string
	submittedMachine string
	injected         chan bool
	done             chan boundaryResult
	accepted         bool
	err              error
}

// Each API wrapper pauses BEFORE the operation. Its result is captured after the
// real operation completes and before Go resumes with that snapshot. Internal
// branches can run ahead, but the next effect cannot pass another boundary.
type pausedProcess struct {
	h           *harness
	publisher   *controllerdemo.MACReconciler
	boundaries  chan *boundary
	invocation  *call
	claim       controllerdemo.Assignment
	readVersion string
	apiError    bool
	returnError error
}

func newPausedProcess(mutation string) (*pausedProcess, error) {
	h, err := newHarness()
	if err != nil {
		return nil, err
	}
	p := &pausedProcess{h: h, boundaries: make(chan *boundary)}
	publisher := *h.mac
	direct := publisher.APIReader
	if mutation == "cached-authorization" {
		direct = publisher.Client
	}
	if mutation == "skip-revalidation" {
		publisher.AllowStalePublication = true
	}

	registry, err := effect.NewRegistry([]effect.Descriptor{
		{ID: "readClaim", Resource: effect.Resource{Group: groupVersion.Group, Version: groupVersion.Version, Kind: "MachineClaim"}, Operation: "get", Consistency: "direct"},
		{ID: "readInfra", Resource: effect.Resource{Group: groupVersion.Group, Version: groupVersion.Version, Kind: "InfraMachine"}, Operation: "get", Consistency: "cached"},
		{ID: "updateStatus", Resource: effect.Resource{Group: groupVersion.Group, Version: groupVersion.Version, Kind: "InfraMachine"}, Operation: "status.update", Consistency: "direct"},
	})
	if err != nil {
		h.close()
		return nil, err
	}
	observer := cr.Observer{Registry: registry, Scheme: h.api.Scheme(), Hook: p.pause}
	publisher.APIReader = observer.Reader(direct, "direct")
	publisher.Client = observer.Client(publisher.Client, "cached")
	p.publisher = &publisher
	return p, nil
}

// Domain projection only; the reusable Observer owns interception and snapshots.
func (p *pausedProcess) pause(ctx context.Context, call effect.Call, execute func() effect.Result) effect.Result {
	b := &boundary{kind: call.Descriptor.ID, injected: make(chan bool), done: make(chan boundaryResult, 1)}
	b.submittedMachine, _, _ = unstructured.NestedString(call.Submitted, "status", "machineId")
	select {
	case p.boundaries <- b:
	case <-ctx.Done():
		return effect.Result{Err: ctx.Err()}
	}
	var fail bool
	select {
	case fail = <-b.injected:
	case <-ctx.Done():
		return effect.Result{Err: ctx.Err()}
	}
	result := effect.Result{}
	if fail {
		result.Err = fmt.Errorf("injected %s failure", b.kind)
	} else {
		result = execute()
	}
	projected := boundaryResult{err: result.Err}
	if result.Err == nil {
		projected.machine, _, _ = unstructured.NestedString(result.Snapshot, "status", "machineId")
		projected.epoch, _, _ = unstructured.NestedInt64(result.Snapshot, "status", "epoch")
		projected.version, _, _ = unstructured.NestedString(result.Snapshot, "metadata", "resourceVersion")
	}
	b.done <- projected
	return result
}

func (p *pausedProcess) next(kind string) (*boundary, error) {
	select {
	case b := <-p.boundaries:
		if b.kind != kind {
			return nil, fmt.Errorf("expected %s boundary, actual Go reached %s", kind, b.kind)
		}
		return b, nil
	case <-p.h.ctx.Done():
		return nil, p.h.ctx.Err()
	}
}
func (p *pausedProcess) begin() error {
	if p.invocation != nil {
		return fmt.Errorf("process already active")
	}
	observed := p.h.observed // immutable argument held by this invocation
	c := &call{done: make(chan error, 1)}
	c.fn = func() error {
		accepted, err := p.publisher.Publish(p.h.ctx, request.NamespacedName, observed)
		b := &boundary{kind: "return", accepted: accepted, err: err}
		select {
		case p.boundaries <- b:
		case <-p.h.ctx.Done():
		}
		return nil
	}
	p.invocation = c
	select {
	case p.h.calls <- *c:
		return nil
	case <-p.h.ctx.Done():
		return p.h.ctx.Err()
	}
}
func (p *pausedProcess) api(kind string, failed bool) (*boundary, boundaryResult, error) {
	b, err := p.next(kind)
	if err != nil {
		return nil, boundaryResult{}, err
	}
	select {
	case b.injected <- failed:
	case <-p.h.ctx.Done():
		return nil, boundaryResult{}, p.h.ctx.Err()
	}
	select {
	case result := <-b.done:
		p.apiError = result.err != nil
		if result.err == nil {
			switch kind {
			case "readClaim":
				p.claim = controllerdemo.Assignment{MachineID: result.machine, Epoch: result.epoch}
			case "readInfra":
				p.readVersion = result.version
			}
		}
		return b, result, nil
	case <-p.h.ctx.Done():
		return nil, boundaryResult{}, p.h.ctx.Err()
	}
}
func (p *pausedProcess) apply(event string, failed bool) (string, error) {
	switch event {
	case "start":
		if err := p.h.apply(event); err != nil {
			return "", err
		}
		p.h.observed = controllerdemo.Assignment{}
		p.h.accepted = false
		p.claim = controllerdemo.Assignment{}
		p.readVersion = ""
		p.apiError = false
		p.returnError = nil
	case "begin":
		return "", p.begin()
	case "readClaim", "readInfra", "commit", "writeError":
		kind := event
		if kind == "commit" || kind == "writeError" {
			kind = "updateStatus"
		}
		b, result, err := p.api(kind, failed)
		if err != nil {
			return "", err
		}
		if kind == "updateStatus" {
			return b.submittedMachine, nil
		}
		if kind == "readInfra" && result.err == nil {
			return result.machine, nil
		}
	case "return":
		b, err := p.next("return")
		if err != nil {
			return "", err
		}
		p.h.accepted = b.accepted
		p.returnError = b.err
		select {
		case <-p.invocation.done:
			p.invocation = nil
		case <-p.h.ctx.Done():
			return "", p.h.ctx.Err()
		}
	case "finish":
		// Forward the actual method error to the real worker; do not fabricate a result from the oracle.
		if !p.h.processing || p.invocation != nil {
			return "", fmt.Errorf("finish before method return")
		}
		select {
		case p.h.replies <- response{err: p.returnError}:
		case <-p.h.ctx.Done():
			return "", p.h.ctx.Err()
		}
		if err := p.h.worker.Wait(); err != nil {
			return "", err
		}
		p.h.processing = false
	case "branch", "setField": // Internal implementation steps are unobservable stuttering.
	case "infra-change":
		obj := &apiv1.InfraMachine{}
		if err := p.h.api.Get(p.h.ctx, request.NamespacedName, obj); err != nil {
			return "", err
		}
		obj.Status.MachineID = ""
		return "", p.h.api.Status().Update(p.h.ctx, obj)
	default:
		return "", p.h.apply(event)
	}
	return "", nil
}
func assignmentProjection(a controllerdemo.Assignment) map[string]any {
	return map[string]any{"machineId": a.MachineID, "epoch": a.Epoch}
}
func ReplayComposition(trace architecture.Trace) (int, error) { return replayComposition(trace, "") }
func replayComposition(trace architecture.Trace, mutation string) (steps int, err error) {
	p, err := newPausedProcess(mutation)
	if err != nil {
		return 0, err
	}
	defer func() {
		closeErr := p.h.close()
		if err == nil {
			err = closeErr
		}
	}()
	for i, state := range trace.States {
		event, ok := state["event"].(string)
		if !ok {
			return i, fmt.Errorf("missing event")
		}
		actualLocal := ""
		if i > 0 {
			failed, ok := state["injected"].(bool)
			if !ok {
				return i, fmt.Errorf("missing failure decision")
			}
			actualLocal, err = p.apply(event, failed)
			if err != nil {
				return i, fmt.Errorf("step %d %s: %w", i, event, err)
			}
		}
		got, err := p.h.projection()
		if err != nil {
			return i, err
		}
		want, ok := state["s"].(map[string]any)
		if !ok {
			return i, fmt.Errorf("missing runtime state")
		}
		for k, v := range got {
			if !reflect.DeepEqual(want[k], v) {
				return i, fmt.Errorf("step %d %s: %s expected %v got %v", i, event, k, want[k], v)
			}
		}
		infra := &apiv1.InfraMachine{}
		if err = p.h.api.Get(p.h.ctx, request.NamespacedName, infra); err != nil {
			return i, err
		}
		values := map[string]any{"observed": assignmentProjection(p.h.observed), "claim": assignmentProjection(p.claim), "err": p.apiError, "returnedError": p.returnError != nil, "publishedMachineId": infra.Status.MachineID, "cachedMachineId": p.h.cache.infra.Status.MachineID}
		for k, v := range values {
			if !reflect.DeepEqual(state[k], v) {
				return i, fmt.Errorf("step %d %s: %s expected %v got %v", i, event, k, state[k], v)
			}
		}
		readVersion, ok1 := state["readRevision"].(int64)
		apiVersion, ok2 := state["apiRevision"].(int64)
		if !ok1 || !ok2 {
			return i, fmt.Errorf("missing version abstraction")
		}
		if (readVersion == apiVersion) != (p.readVersion == infra.ResourceVersion) {
			return i, fmt.Errorf("step %d %s: retained resourceVersion differs", i, event)
		}
		if event == "commit" || event == "writeError" || (event == "readInfra" && !p.apiError) {
			if state["localMachineId"] != actualLocal {
				return i, fmt.Errorf("step %d %s: local machine ID expected %v got %v", i, event, state["localMachineId"], actualLocal)
			}
		}
	}
	return len(trace.States) - 1, nil
}
