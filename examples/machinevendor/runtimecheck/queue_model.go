package runtimecheck

import cr "generative-architecture/pkg/controllerruntime"

func queueHelpers(p Profile) string {
	return cr.QueueHelpers(cr.QueueProfile{RetryDelayTicks: p.RetryDelayTicks, RequeueAfterTicks: p.RequeueAfterTicks})
}
