// Package tenant reconciles Tenants: each Tenant gets a Namespace of the
// same name and a ResourceQuota inside it.
package tenant

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/phoban01/generative-architecture/examples/refsystem/api/v1alpha1"
)

// QuotaName is the name of the ResourceQuota in each tenant namespace.
const QuotaName = "tenant-quota"

// TenantReconciler creates the Namespace and ResourceQuota of a Tenant.
type TenantReconciler struct {
	Client client.Client
}

// Reconcile creates the tenant's Namespace and ResourceQuota when they are
// missing, then marks the Tenant's namespace ready.
func (r *TenantReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	tenant := &v1alpha1.Tenant{}
	err := r.Client.Get(ctx, req.NamespacedName, tenant)
	if apierrors.IsNotFound(err) {
		return reconcile.Result{}, nil
	}
	if err != nil {
		return reconcile.Result{}, err
	}

	namespace := &corev1.Namespace{}
	err = r.Client.Get(ctx, client.ObjectKey{Name: tenant.Name}, namespace)
	if apierrors.IsNotFound(err) {
		namespace.Name = tenant.Name
		if err := r.Client.Create(ctx, namespace); err != nil {
			return reconcile.Result{}, err
		}
	} else if err != nil {
		return reconcile.Result{}, err
	}

	// The quota records the tenant's profile as a label.
	quota := &corev1.ResourceQuota{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: tenant.Name, Name: QuotaName}, quota)
	if apierrors.IsNotFound(err) {
		quota.Namespace = tenant.Name
		quota.Name = QuotaName
		quota.Labels = map[string]string{"profile": tenant.Spec.Quota}
		if err := r.Client.Create(ctx, quota); err != nil {
			return reconcile.Result{}, err
		}
	} else if err != nil {
		return reconcile.Result{}, err
	}

	if tenant.Status.NamespaceReady {
		return reconcile.Result{}, nil
	}
	tenant.Status.NamespaceReady = true
	return reconcile.Result{}, r.Client.Status().Update(ctx, tenant)
}

// SetupWithManager registers the controller with the manager.
func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Tenant{}).
		Owns(&corev1.Namespace{}).
		Complete(r)
}
