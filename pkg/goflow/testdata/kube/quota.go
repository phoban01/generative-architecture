package kube

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Quota sets hard limits from constant quantities.
func (r *Reconciler) Quota(ctx context.Context) (reconcile.Result, error) {
	quota := &corev1.ResourceQuota{}
	quota.Spec.Hard = corev1.ResourceList{
		corev1.ResourceCPU:            resource.MustParse("1000m"),
		corev1.ResourcePods:           *resource.NewQuantity(10, resource.DecimalSI),
		corev1.ResourceRequestsMemory: resource.MustParse("1Gi"),
	}
	return reconcile.Result{}, nil
}

// QuotaFrom parses a quantity that is not a constant.
func (r *Reconciler) QuotaFrom(ctx context.Context, cm *corev1.ConfigMap) (reconcile.Result, error) {
	quota := &corev1.ResourceQuota{}
	quota.Spec.Hard = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cm.Name)}
	return reconcile.Result{}, nil
}
