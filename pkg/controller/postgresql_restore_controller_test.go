package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
)

func TestDBRestoreCreatesOnlyNewInstanceAndWaitsForPrimary(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	source := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"}, Spec: v1.KDBInstanceSpec{Engine: "postgresql", EngineVersion: "17", PostgreSQL: &v1.PostgreSQLSpec{Backups: &v1.PostgreSQLBackupSpec{PGBackRest: &v1.PostgreSQLPGBackRestSpec{Enabled: true, RepoType: "s3", S3Bucket: "backups"}}}}}
	backup := &v1.DBBackup{ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "kdb"}, Spec: v1.DBBackupSpec{OperationID: "b", InstanceRef: corev1.LocalObjectReference{Name: "orders"}, Type: "full"}, Status: v1.DBBackupStatus{Phase: v1.DBBackupPhaseSucceeded, Artifact: &v1.DBBackupArtifactStatus{BackupID: "20260715F"}}}
	restore := &v1.DBRestore{ObjectMeta: metav1.ObjectMeta{Name: "restore", Namespace: "kdb"}, Spec: v1.DBRestoreSpec{OperationID: "r", BackupRef: corev1.LocalObjectReference{Name: "backup"}, Mode: "NewInstance", TargetInstanceName: "orders-restored", Target: v1.DBRestoreTarget{Type: "lsn", Value: "0/200"}}}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(source, backup, restore).Build()
	reconciler := &DBRestoreReconciler{Client: client, Scheme: scheme}
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "kdb", Name: "restore"}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	target := &v1.KDBInstance{}
	if err := client.Get(context.Background(), types.NamespacedName{Namespace: "kdb", Name: "orders-restored"}, target); err != nil {
		t.Fatal(err)
	}
	if target.Spec.PostgreSQL == nil || target.Spec.PostgreSQL.Restore == nil || target.Spec.PostgreSQL.Restore.BackupID != "20260715F" || target.Spec.PostgreSQL.Restore.TargetType != "lsn" || target.Spec.PostgreSQL.Restore.TargetAction != "promote" {
		t.Fatalf("target restore %#v", target.Spec.PostgreSQL)
	}
	target.Status.PostgreSQL = &v1.PostgreSQLStatus{Ready: true, Primary: "orders-restored0-0"}
	if err := client.Update(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	got := &v1.DBRestore{}
	if err := client.Get(context.Background(), request.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != v1.DBRestorePhaseSucceeded {
		t.Fatalf("restore status %#v", got.Status)
	}
}

func TestDBRestoreRejectsInPlace(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	restore := &v1.DBRestore{ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: "kdb"}, Spec: v1.DBRestoreSpec{OperationID: "r", Mode: "InPlace", TargetInstanceName: "orders"}}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(restore).Build()
	r := &DBRestoreReconciler{Client: client, Scheme: scheme}
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "kdb", Name: "bad"}}
	if _, err := r.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	got := &v1.DBRestore{}
	_ = client.Get(context.Background(), request.NamespacedName, got)
	if got.Status.Phase != v1.DBRestorePhaseFailed {
		t.Fatalf("status %#v", got.Status)
	}
}

func TestPostgresRestoreTargetTimeNormalizesRFC3339(t *testing.T) {
	if got := postgresRestoreTargetTime("2026-07-15T10:51:13Z"); got != "2026-07-15 10:51:13+00:00" {
		t.Fatalf("restore target = %q", got)
	}
}
