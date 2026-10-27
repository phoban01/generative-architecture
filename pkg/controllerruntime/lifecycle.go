package controllerruntime

import (
	"context"
	"fmt"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// syncSource is a controller-runtime syncing source that delivers no events
// and syncs when the driver calls Controller.Sync. controller-runtime waits
// for it before it starts workers, as it waits for informer caches.
type syncSource struct {
	group  *Group
	synced chan struct{}
	done   bool // driver-side view of synced
}

func (s *syncSource) Start(context.Context, workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
	return nil
}

// WaitForSync returns when the driver syncs. It also returns without error
// when the group closes, so a trace that ends before sync shuts down cleanly.
func (s *syncSource) WaitForSync(ctx context.Context) error {
	select {
	case <-s.synced:
		return nil
	case <-s.group.ctx.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *syncSource) String() string { return "architecture-conformance-sync" }

// Sync marks the controller's caches synced, so controller-runtime starts
// its workers. Add the initial list with EnqueueLow before Sync.
func (c *Controller) Sync() error {
	if c.sync == nil {
		return fmt.Errorf("controller %s has no startup source", c.name)
	}
	if c.sync.done {
		return fmt.Errorf("controller %s already synced", c.name)
	}
	c.sync.done = true
	close(c.sync.synced)
	return nil
}

// Synced reports whether the controller's caches synced. A controller
// without Startup counts as synced.
func (c *Controller) Synced() bool { return c.sync == nil || c.sync.done }
