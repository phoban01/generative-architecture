// Package backup reconciles Backups: once a Backup's Database is ready, it
// runs a Job that backs the Database up and reports the Job's progress.
package backup

import (
	"context"
	"time"

	batchv1 "k8s.io/api/batch/v1"
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

// Image is the container image of the backup Job.
const Image = "busybox:1.36"

// Backup phases.
const (
	PhasePending   = "Pending"
	PhaseRunning   = "Running"
	PhaseCompleted = "Completed"
	PhaseFailed    = "Failed"
)

// BackupReconciler runs the backup Job of a Backup.
type BackupReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// Reconcile waits for the Backup's Database to be ready, creates the backup
// Job when it is missing and sets the Backup's phase from the Job's status.
// The Job has the Backup's name and the Backup controls it. An unready
// Database is not requeued: the Database watch enqueues the Backup again
// when the Database changes. A running Job is checked again after 30
// seconds, in case a Job event is missed.
func (r *BackupReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	backup := &v1alpha1.Backup{}
	err := r.Client.Get(ctx, req.NamespacedName, backup)
	if apierrors.IsNotFound(err) {
		return reconcile.Result{}, nil
	}
	if err != nil {
		return reconcile.Result{}, err
	}

	database := &v1alpha1.Database{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: backup.Namespace, Name: backup.Spec.Database}, database)
	if apierrors.IsNotFound(err) {
		return reconcile.Result{}, nil
	}
	if err != nil {
		return reconcile.Result{}, err
	}
	if !database.Status.Ready {
		return reconcile.Result{}, nil
	}

	job := &batchv1.Job{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: backup.Namespace, Name: backup.Name}, job)
	if apierrors.IsNotFound(err) {
		job.Namespace = backup.Namespace
		job.Name = backup.Name
		job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyNever
		job.Spec.Template.Spec.Containers = []corev1.Container{{Name: "backup", Image: Image, Args: []string{"echo", database.Name}}}
		if err := controllerutil.SetControllerReference(backup, job, r.Scheme); err != nil {
			return reconcile.Result{}, err
		}
		if err := r.Client.Create(ctx, job); err != nil {
			return reconcile.Result{}, err
		}
	} else if err != nil {
		return reconcile.Result{}, err
	}

	switch {
	case job.Status.Succeeded > 0:
		backup.Status.Phase = PhaseCompleted
	case job.Status.Failed > 0:
		backup.Status.Phase = PhaseFailed
	case job.Status.Active > 0:
		backup.Status.Phase = PhaseRunning
	default:
		backup.Status.Phase = PhasePending
	}
	if err := r.Client.Status().Update(ctx, backup); err != nil {
		return reconcile.Result{}, err
	}
	if backup.Status.Phase == PhaseRunning {
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}
	return reconcile.Result{}, nil
}

// SetupWithManager registers the controller with the manager.
func (r *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Backup{}).
		Owns(&batchv1.Job{}).
		Watches(&v1alpha1.Database{}, handler.EnqueueRequestsFromMapFunc(r.databaseToBackups)).
		Complete(r)
}

// databaseToBackups lists the Backups of the Database in its namespace.
func (r *BackupReconciler) databaseToBackups(ctx context.Context, obj client.Object) []reconcile.Request {
	backups := &v1alpha1.BackupList{}
	if err := r.Client.List(ctx, backups, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for _, b := range backups.Items {
		if b.Spec.Database == obj.GetName() {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: b.Namespace, Name: b.Name}})
		}
	}
	return requests
}
