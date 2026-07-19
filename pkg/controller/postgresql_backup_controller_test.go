package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
)

func TestDBBackupReconcilerProjectsRuntimeArtifact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"Succeeded","result":{"backupId":"20260715F","type":"full","stanza":"orders","startTime":"2026-07-15T09:00:00Z","stopTime":"2026-07-15T09:01:00Z","startLsn":"0/100","stopLsn":"0/200","sizeBytes":100,"repoSizeBytes":60}}`))
	}))
	defer server.Close()
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"}, Spec: v1.KDBInstanceSpec{Engine: "postgresql"}, Status: v1.KDBInstanceStatus{PostgreSQL: &v1.PostgreSQLStatus{Ready: true, Primary: "orders0-0", Endpoints: []v1.HostInfo{{PodName: "orders0-0", Host: "127.0.0.1"}}, PGBackRest: &v1.PostgreSQLPGBackRestStatus{Enabled: true}}}}
	backup := &v1.DBBackup{ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "kdb"}, Spec: v1.DBBackupSpec{OperationID: "op", InstanceRef: corev1.LocalObjectReference{Name: "orders"}, Type: "full", Validate: true}}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, backup).Build()
	reconciler := &DBBackupReconciler{Client: client, Scheme: scheme, HTTPClient: server.Client(), Endpoint: func(*v1.KDBInstance) string { return server.URL }}
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "kdb", Name: "backup"}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	got := &v1.DBBackup{}
	if err := client.Get(context.Background(), request.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != v1.DBBackupPhaseSucceeded || got.Status.ExecutorPod != "orders0-0" || got.Status.Artifact == nil || got.Status.Artifact.BackupID != "20260715F" || got.Status.ValidationStatus != "Passed" {
		t.Fatalf("backup status %#v", got.Status)
	}
}

func TestDBBackupRuntimeEndpointStaysOnExecutorPod(t *testing.T) {
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"}, Status: v1.KDBInstanceStatus{PostgreSQL: &v1.PostgreSQLStatus{
		Primary: "orders1-0",
		Endpoints: []v1.HostInfo{
			{PodName: "orders0-0", Host: "10.0.0.1"},
			{PodName: "orders1-0", Host: "10.0.0.2"},
		},
	}}}
	reconciler := &DBBackupReconciler{}
	if got := reconciler.runtimeEndpoint(instance, "orders0-0"); got != "https://orders0-0.orders.kdb.svc:8008" {
		t.Fatalf("runtime endpoint = %q", got)
	}
}
