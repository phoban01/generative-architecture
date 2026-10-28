package backup

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/phoban01/generative-architecture/examples/refsystem/api/v1alpha1"
	"github.com/phoban01/generative-architecture/examples/refsystem/internal/extractcheck"
	"github.com/phoban01/generative-architecture/pkg/ir"
)

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(&v1alpha1.Backup{}, &v1alpha1.Database{}).Build()
}

func database(ready bool) *v1alpha1.Database {
	return &v1alpha1.Database{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "orders"}, Status: v1alpha1.DatabaseStatus{Ready: ready}}
}

func backup() *v1alpha1.Backup {
	return &v1alpha1.Backup{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "nightly"}, Spec: v1alpha1.BackupSpec{Database: "orders"}}
}

var key = client.ObjectKey{Namespace: "default", Name: "nightly"}

func reconcileBackup(t *testing.T, c client.Client) *v1alpha1.Backup {
	t.Helper()
	r := &BackupReconciler{Client: c}
	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key})
	if err != nil {
		t.Fatal(err)
	}
	if res != (reconcile.Result{}) {
		t.Fatalf("unexpected result %+v", res)
	}
	got := &v1alpha1.Backup{}
	if err := c.Get(context.Background(), key, got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReconcileWaitsForDatabase(t *testing.T) {
	for name, objs := range map[string][]client.Object{
		"missing": {backup()},
		"unready": {backup(), database(false)},
	} {
		t.Run(name, func(t *testing.T) {
			c := newClient(t, objs...)
			if got := reconcileBackup(t, c); got.Status.Phase != "" {
				t.Fatalf("phase %q before the database is ready", got.Status.Phase)
			}
			jobs := &batchv1.JobList{}
			if err := c.List(context.Background(), jobs); err != nil {
				t.Fatal(err)
			}
			if len(jobs.Items) != 0 {
				t.Fatalf("jobs created early: %d", len(jobs.Items))
			}
		})
	}
}

func TestReconcileCreatesJob(t *testing.T) {
	c := newClient(t, backup(), database(true))
	if got := reconcileBackup(t, c); got.Status.Phase != PhasePending {
		t.Fatalf("phase %q, want %q", got.Status.Phase, PhasePending)
	}
	job := &batchv1.Job{}
	if err := c.Get(context.Background(), key, job); err != nil {
		t.Fatal(err)
	}
	containers := job.Spec.Template.Spec.Containers
	if len(containers) != 1 || containers[0].Image != Image || job.Spec.Template.Spec.RestartPolicy != "Never" {
		t.Fatalf("job spec %+v", job.Spec.Template.Spec)
	}
}

func TestReconcileSetsPhaseFromJob(t *testing.T) {
	for _, tc := range []struct {
		status batchv1.JobStatus
		phase  string
	}{
		{batchv1.JobStatus{}, PhasePending},
		{batchv1.JobStatus{Active: 1}, PhaseRunning},
		{batchv1.JobStatus{Succeeded: 1}, PhaseCompleted},
		{batchv1.JobStatus{Failed: 1}, PhaseFailed},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "nightly"}, Status: tc.status}
			c := newClient(t, backup(), database(true), job)
			if got := reconcileBackup(t, c); got.Status.Phase != tc.phase {
				t.Fatalf("phase %q, want %q", got.Status.Phase, tc.phase)
			}
		})
	}
}

func TestDatabaseToBackups(t *testing.T) {
	other := backup()
	other.Name, other.Spec.Database = "unrelated", "other"
	r := &BackupReconciler{Client: newClient(t, backup(), other)}
	got := r.databaseToBackups(context.Background(), database(true))
	if len(got) != 1 || got[0].NamespacedName != key {
		t.Fatalf("requests %v", got)
	}
}

// TestExtraction checks that the extractor reads the whole controller.
// Reconcile extracts: the Job's containers are a slice literal that writes
// no model state. The Database watch maps through the Backups whose
// spec.database names the Database. The test fails when a new gap appears.
func TestExtraction(t *testing.T) {
	res, err := extractcheck.Run("./examples/refsystem/controllers/backup", "BackupReconciler")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Controllers) != 1 || res.Controllers[0].Name != "BackupReconciler" {
		t.Fatalf("controllers %+v", res.Controllers)
	}
	if res.Program == nil || len(res.Program.Nodes) == 0 {
		t.Fatal("Reconcile not extracted")
	}
	type gap struct{ construct, source, reason string }
	want := []gap{}
	if len(res.Diagnostics) != len(want) {
		t.Fatalf("diagnostics %v, want %d", res.Diagnostics, len(want))
	}
	for i, d := range res.Diagnostics {
		if got := (gap{d.Construct, d.Source, d.Reason}); got != want[i] {
			t.Errorf("diagnostic %d: %+v, want %+v", i, got, want[i])
		}
	}
	watches := res.Watches.IR("backup", "controller.go")
	if len(watches) != 3 {
		t.Fatalf("watches %+v, want 3", watches)
	}
	m := watches[2].Mapping
	if watches[2].Resource.Kind != "Database" || m.Kind != ir.MappingReference || strings.Join(m.DependentPath, ".") != "Spec.Database" || m.Dependent == nil || m.Dependent.Kind != "Backup" {
		t.Fatalf("database watch %+v, mapping %+v", watches[2], m)
	}
}
