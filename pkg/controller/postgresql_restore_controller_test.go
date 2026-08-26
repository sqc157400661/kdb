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
	"github.com/sqc157400661/kdb/internal/naming"
)

func TestDBRestoreCreatesOnlyNewInstanceAndWaitsForPrimary(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	source := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{
		Name: "orders", Namespace: "kdb",
		Labels: map[string]string{
			"kdb.io/instance-id":         "orders",
			naming.LabelScopeTenant:      "default",
			naming.LabelScopeProject:     "source",
			naming.LabelScopeEnvironment: "prod",
			naming.LabelScopeRegion:      naming.KubernetesLabelValue("volcengine/cn-beijing"),
			naming.LabelScopeInstance:    "orders",
		},
		Annotations: map[string]string{
			naming.AnnotationRawTenant:      "default",
			naming.AnnotationRawProject:     "source",
			naming.AnnotationRawEnvironment: "prod",
			naming.AnnotationRawRegion:      "volcengine/cn-beijing",
			naming.AnnotationRawInstance:    "orders",
			naming.AnnotationScopeRevision:  "16",
			naming.AnnotationScopeHash:      "sha256:source",
		},
	}, Spec: v1.KDBInstanceSpec{Engine: "postgresql", EngineVersion: "17", Leader: v1.HostInfo{PodName: "orders0-0"}, PostgreSQL: &v1.PostgreSQLSpec{Backups: &v1.PostgreSQLBackupSpec{PGBackRest: &v1.PostgreSQLPGBackRestSpec{Enabled: true, RepoType: "s3", S3Bucket: "backups"}}}}}
	backup := &v1.DBBackup{ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "kdb"}, Spec: v1.DBBackupSpec{OperationID: "b", InstanceRef: corev1.LocalObjectReference{Name: "orders"}, Type: "full"}, Status: v1.DBBackupStatus{Phase: v1.DBBackupPhaseSucceeded, Artifact: &v1.DBBackupArtifactStatus{BackupID: "20260715F"}}}
	restore := &v1.DBRestore{ObjectMeta: metav1.ObjectMeta{
		Name: "restore", Namespace: "kdb",
		Labels: map[string]string{
			naming.LabelScopeTenant:      "default",
			naming.LabelScopeProject:     "target",
			naming.LabelScopeEnvironment: "stage",
			naming.LabelScopeRegion:      naming.KubernetesLabelValue("volcengine/cn-shanghai"),
			naming.LabelScopeInstance:    "orders-restored",
		},
		Annotations: map[string]string{
			naming.AnnotationRawTenant:      "default",
			naming.AnnotationRawProject:     "target",
			naming.AnnotationRawEnvironment: "stage",
			naming.AnnotationRawRegion:      "volcengine/cn-shanghai",
			naming.AnnotationRawInstance:    "orders-restored",
			naming.AnnotationScopeRevision:  "17",
			naming.AnnotationScopeHash:      "sha256:target",
		},
	}, Spec: v1.DBRestoreSpec{OperationID: "r", BackupRef: corev1.LocalObjectReference{Name: "backup"}, Mode: "NewInstance", TargetInstanceName: "orders-restored", Target: v1.DBRestoreTarget{Type: "lsn", Value: "0/200"}}}
	sourceCredential := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-postgresql-credential", Namespace: "kdb"},
		Data:       map[string][]byte{"postgres-password": []byte("test-only")},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(source, backup, restore, sourceCredential).Build()
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
	if target.Spec.Leader.PodName != "" || target.Spec.Shutdown == nil || *target.Spec.Shutdown ||
		target.Spec.PostgreSQL.CredentialSecretRef == nil || target.Spec.PostgreSQL.CredentialSecretRef.Name != "orders-restored-postgresql-credential" ||
		target.Labels["kdb.io/instance-id"] != "orders-restored" {
		t.Fatalf("target identity was not normalized: metadata=%#v spec=%#v", target.ObjectMeta, target.Spec)
	}
	if target.Labels[naming.LabelScopeProject] != "target" ||
		target.Labels[naming.LabelScopeInstance] != "orders-restored" ||
		target.Annotations[naming.AnnotationRawProject] != "target" ||
		target.Annotations[naming.AnnotationScopeRevision] != "17" {
		t.Fatalf("target scope identity was not inherited from DBRestore: metadata=%#v", target.ObjectMeta)
	}
	targetCredential := &corev1.Secret{}
	if err := client.Get(context.Background(), types.NamespacedName{Namespace: "kdb", Name: "orders-restored-postgresql-credential"}, targetCredential); err != nil {
		t.Fatal(err)
	}
	if string(targetCredential.Data["postgres-password"]) != "test-only" ||
		len(targetCredential.OwnerReferences) != 1 || targetCredential.OwnerReferences[0].Name != target.Name {
		t.Fatalf("target credential was not cloned independently: metadata=%#v keys=%d", targetCredential.ObjectMeta, len(targetCredential.Data))
	}
	target.Status.PostgreSQL = &v1.PostgreSQLStatus{Ready: true, Primary: "orders-restored0-0"}
	target.Status.Conditions = []metav1.Condition{{
		Type:               v1.PostgreSQLAvailable,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: target.Generation,
	}}
	if err := client.Update(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := client.Get(context.Background(), types.NamespacedName{Namespace: "kdb", Name: "orders-restored"}, target); err != nil {
		t.Fatal(err)
	}
	if target.Spec.PostgreSQL.Restore != nil {
		t.Fatalf("restore bootstrap was not cleared: %#v", target.Spec.PostgreSQL.Restore)
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

func TestDBRestoreLocalRepositoryUsesBackupExecutorPVC(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	source := &v1.KDBInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"},
		Spec: v1.KDBInstanceSpec{
			Engine: "postgresql", EngineVersion: "17",
			PostgreSQL: &v1.PostgreSQLSpec{Backups: &v1.PostgreSQLBackupSpec{PGBackRest: &v1.PostgreSQLPGBackRestSpec{Enabled: true, RepoType: "local"}}},
		},
	}
	backup := &v1.DBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "kdb"},
		Spec:       v1.DBBackupSpec{InstanceRef: corev1.LocalObjectReference{Name: source.Name}},
		Status: v1.DBBackupStatus{
			Phase:       v1.DBBackupPhaseSucceeded,
			ExecutorPod: "orders2-0",
			Artifact:    &v1.DBBackupArtifactStatus{BackupID: "20260715F"},
		},
	}
	restore := &v1.DBRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "restore", Namespace: "kdb"},
		Spec:       v1.DBRestoreSpec{OperationID: "r", BackupRef: corev1.LocalObjectReference{Name: backup.Name}, Mode: "NewInstance", TargetInstanceName: "orders-restored"},
	}
	sourceCredential := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-postgresql-credential", Namespace: "kdb"},
		Data:       map[string][]byte{"postgres-password": []byte("test-only")},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(source, backup, restore, sourceCredential).Build()
	reconciler := &DBRestoreReconciler{Client: client, Scheme: scheme}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "kdb", Name: "restore"}}); err != nil {
		t.Fatal(err)
	}
	target := &v1.KDBInstance{}
	if err := client.Get(context.Background(), types.NamespacedName{Namespace: "kdb", Name: "orders-restored"}, target); err != nil {
		t.Fatal(err)
	}
	if got := target.Annotations[restoreLocalRepositoryPVCAnnotation]; got != "orders2-kdb-data" {
		t.Fatalf("restore repository PVC=%q, want orders2-kdb-data", got)
	}
}

func TestSucceededDBRestoreDetachesBootstrapWithoutSourceInstance(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	target := &v1.KDBInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "orders-restored",
			Namespace:   "kdb",
			Annotations: map[string]string{restoreOperationAnnotation: "r", restoreLocalRepositoryPVCAnnotation: "orders0-kdb-data"},
		},
		Spec: v1.KDBInstanceSpec{
			Engine: "postgresql",
			PostgreSQL: &v1.PostgreSQLSpec{
				Restore: &v1.PostgreSQLRestoreBootstrapSpec{OperationID: "r", BackupID: "20260715F"},
			},
		},
	}
	restore := &v1.DBRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "restore", Namespace: "kdb"},
		Spec:       v1.DBRestoreSpec{OperationID: "r", Mode: "NewInstance", TargetInstanceName: target.Name},
		Status: v1.DBRestoreStatus{
			Phase:             v1.DBRestorePhaseSucceeded,
			TargetInstanceRef: &corev1.LocalObjectReference{Name: target.Name},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(target, restore).Build()
	reconciler := &DBRestoreReconciler{Client: client, Scheme: scheme}
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "kdb", Name: restore.Name}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	got := &v1.KDBInstance{}
	if err := client.Get(context.Background(), types.NamespacedName{Namespace: "kdb", Name: target.Name}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.PostgreSQL == nil || got.Spec.PostgreSQL.Restore != nil {
		t.Fatalf("restore bootstrap was not removed: %#v", got.Spec.PostgreSQL)
	}
	if got.Annotations[restoreLocalRepositoryPVCAnnotation] != "" {
		t.Fatalf("source repository PVC annotation was not removed: %#v", got.Annotations)
	}
	if got.Annotations[restoreOperationAnnotation] != "r" {
		t.Fatalf("restore identity annotation changed: %#v", got.Annotations)
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
