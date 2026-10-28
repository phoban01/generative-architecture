// Package database reconciles Databases: once a Database's Tenant has a
// ready namespace, it creates the credentials Secret of the Database.
package database

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/phoban01/generative-architecture/examples/refsystem/api/v1alpha1"
)

// TenantLabel marks a Database with the name of its Tenant, so that a
// change to the Tenant can find its Databases.
const TenantLabel = "refsystem.example.org/tenant"

// DatabaseReconciler creates the credentials Secret of a Database.
type DatabaseReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// Reconcile waits for the Database's Tenant to be ready, creates the
// credentials Secret when it is missing and records the Secret in the
// Database's status. The Secret lives beside the Database, because an
// owner reference cannot cross namespaces, and the Database controls it.
func (r *DatabaseReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	database := &v1alpha1.Database{}
	err := r.Client.Get(ctx, req.NamespacedName, database)
	if apierrors.IsNotFound(err) {
		return reconcile.Result{}, nil
	}
	if err != nil {
		return reconcile.Result{}, err
	}

	// A missing or unready Tenant is not an error: the Tenant watch
	// enqueues this Database again when the Tenant changes.
	tenant := &v1alpha1.Tenant{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: database.Namespace, Name: database.Spec.Tenant}, tenant)
	if apierrors.IsNotFound(err) {
		return reconcile.Result{}, nil
	}
	if err != nil {
		return reconcile.Result{}, err
	}
	if !tenant.Status.NamespaceReady {
		return reconcile.Result{}, nil
	}

	// The Secret has the Database's name. database.Name + "-credentials"
	// would be clearer, but the extractor cannot yet lower a string
	// concatenation with a field.
	secret := &corev1.Secret{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: database.Namespace, Name: database.Name}, secret)
	if apierrors.IsNotFound(err) {
		secret.Namespace = database.Namespace
		secret.Name = database.Name
		secret.StringData = map[string]string{"username": database.Name}
		if err := controllerutil.SetControllerReference(database, secret, r.Scheme); err != nil {
			return reconcile.Result{}, err
		}
		if err := r.Client.Create(ctx, secret); err != nil {
			return reconcile.Result{}, err
		}
	} else if err != nil {
		return reconcile.Result{}, err
	}

	if database.Status.Ready && database.Status.SecretName == database.Name {
		return reconcile.Result{}, nil
	}
	database.Status.Ready = true
	database.Status.SecretName = database.Name
	return reconcile.Result{}, r.Client.Status().Patch(ctx, database, client.Merge)
}

// SetupWithManager registers the controller with the manager.
func (r *DatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Database{}).
		Owns(&corev1.Secret{}).
		Watches(&v1alpha1.Tenant{}, handler.EnqueueRequestsFromMapFunc(r.tenantToDatabases)).
		Complete(r)
}

// tenantToDatabases lists the Databases labelled with the Tenant's name in
// the Tenant's namespace.
func (r *DatabaseReconciler) tenantToDatabases(ctx context.Context, obj client.Object) []reconcile.Request {
	databases := &v1alpha1.DatabaseList{}
	if err := r.Client.List(ctx, databases, client.InNamespace(obj.GetNamespace()), client.MatchingLabels{TenantLabel: obj.GetName()}); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(databases.Items))
	for _, db := range databases.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: db.Namespace, Name: db.Name}})
	}
	return requests
}
