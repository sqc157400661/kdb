package steps

import (
	stdctx "context"
	"strings"
	"testing"

	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	reconcilecontext "github.com/sqc157400661/kdb/pkg/reconcile/context"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestPostgreSQLPodUsesOnlyKDBHAManagementSidecar(t *testing.T) {
	replicas := int32(1)
	port := int32(5432)
	instance := &v1.KDBInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-pg", Namespace: "kdb"},
		Spec: v1.KDBInstanceSpec{
			Engine: naming.PostgresEngine, EngineVersion: "17", Port: &port,
			InstanceSet: shared.InstanceSetSpec{
				Replicas:         &replicas,
				MainContainer:    shared.ContainerSpec{Image: "postgresql17:test"},
				SidecarContainer: shared.ContainerSpec{Image: "postgresql-sidecar:test", Command: []string{"kdb-ha"}, Args: []string{"/etc/patroni/patroni.yaml"}},
			},
			PostgreSQL: &v1.PostgreSQLSpec{Backups: &v1.PostgreSQLBackupSpec{PGBackRest: &v1.PostgreSQLPGBackRestSpec{Enabled: true, RepoType: "local"}}},
		},
	}
	rc := newPostgreSQLPodTestContext(t, instance)
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: instance.Name, Namespace: instance.Namespace}}
	postgresPodIntent(rc, statefulSet)

	managementCount := 0
	var database, kdbHA *corev1.Container
	for i := range statefulSet.Spec.Template.Spec.Containers {
		container := &statefulSet.Spec.Template.Spec.Containers[i]
		switch container.Name {
		case naming.ContainerDatabase:
			database = container
		case naming.ContainerPostgreSQLHA:
			managementCount++
			kdbHA = container
		case naming.ContainerSidecar:
			t.Fatalf("PostgreSQL pod contains generic sidecar %q", container.Name)
		}
	}
	if database == nil || kdbHA == nil || managementCount != 1 {
		t.Fatalf("unexpected PostgreSQL containers: %#v", statefulSet.Spec.Template.Spec.Containers)
	}
	if kdbHA.Command[0] != "kdb-ha" || kdbHA.Ports[0].Name != naming.PortPostgreSQLHA {
		t.Fatalf("unexpected kdb-ha runtime: %#v", kdbHA)
	}
	if database.Image == kdbHA.Image || database.Command[0] != "kdb-pg-runtime" || kdbHA.Image != "postgresql-sidecar:test" {
		t.Fatalf("database and control plane were not split: database=%#v sidecar=%#v", database, kdbHA)
	}
	if database.Lifecycle == nil || database.Lifecycle.PreStop == nil || database.Lifecycle.PreStop.Exec == nil ||
		strings.Join(database.Lifecycle.PreStop.Exec.Command, " ") != "/usr/lib/postgresql/17/bin/pg_ctl stop -D /pgdata/pg17 -m fast -w" {
		t.Fatalf("database does not own graceful PostgreSQL shutdown: %#v", database.Lifecycle)
	}
	for _, volume := range []string{"postgres-data", "postgres-tmp", "postgres-runtime", "postgres-tls"} {
		if !hasMount(database.VolumeMounts, volume) || !hasMount(kdbHA.VolumeMounts, volume) {
			t.Fatalf("volume %q is not shared by database and kdb-ha", volume)
		}
	}
	if hasMount(database.VolumeMounts, "patroni-config") || !hasMount(kdbHA.VolumeMounts, "patroni-config") {
		t.Fatal("control configuration must be mounted only in the kdb-ha sidecar")
	}
	if hasMountPath(database.VolumeMounts, "/backrestrepo") || !hasMountPath(kdbHA.VolumeMounts, "/backrestrepo") {
		t.Fatal("pgBackRest repository must be mounted only in the kdb-ha sidecar")
	}
	for _, name := range []string{"PATRONI_POSTGRESQL_AUTHENTICATION_SUPERUSER_PASSWORD", "PATRONI_RESTAPI_PASSWORD", "KDB_HA_POSTGRESQL_RUNTIME_SOCKET", "KDB_HA_POSTGRESQL_TOOL_SOCKET"} {
		if hasEnv(database.Env, name) || !hasEnv(kdbHA.Env, name) {
			t.Fatalf("control environment %q must be injected only into kdb-ha", name)
		}
	}
	startupCommand := statefulSet.Spec.Template.Spec.InitContainers[0].Command[2]
	if !strings.Contains(startupCommand, "/pgdata/pg17/pg_wal") || strings.Contains(startupCommand, "/pgwal/pg17_wal") {
		t.Fatalf("WAL without a dedicated PVC must remain in PGDATA: %s", startupCommand)
	}
}

func TestPostgreSQLExporterReceivesTLSAndExtendedQueryConfig(t *testing.T) {
	replicas, port := int32(1), int32(5432)
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "observed-pg", Namespace: "kdb"}, Spec: v1.KDBInstanceSpec{
		Engine: naming.PostgresEngine, EngineVersion: "17", Port: &port,
		InstanceSet: shared.InstanceSetSpec{
			Replicas:         &replicas,
			MainContainer:    shared.ContainerSpec{Image: "postgresql17:test"},
			SidecarContainer: shared.ContainerSpec{Image: "kdb-ha:test"},
			MonitorContainer: shared.ContainerSpec{Image: "postgres-exporter:test"},
		},
		PostgreSQL: &v1.PostgreSQLSpec{Exporter: &v1.PostgreSQLExporterSpec{Enabled: true}},
	}}
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: instance.Name, Namespace: instance.Namespace}}
	postgresPodIntent(newPostgreSQLPodTestContext(t, instance), statefulSet)
	var exporter *corev1.Container
	for i := range statefulSet.Spec.Template.Spec.Containers {
		if statefulSet.Spec.Template.Spec.Containers[i].Name == naming.ContainerPostgreSQLExporter {
			exporter = &statefulSet.Spec.Template.Spec.Containers[i]
			break
		}
	}
	if exporter == nil {
		t.Fatal("postgresql exporter container is missing")
	}
	for _, volume := range []string{"postgres-tmp", "postgres-tls", "patroni-config"} {
		if !hasMount(exporter.VolumeMounts, volume) {
			t.Fatalf("exporter mount %q is missing: %#v", volume, exporter.VolumeMounts)
		}
	}
	if !hasEnv(exporter.Env, "PG_EXPORTER_EXTEND_QUERY_PATH") || !hasEnv(exporter.Env, "DATA_SOURCE_URI") {
		t.Fatalf("exporter collection environment is incomplete: %#v", exporter.Env)
	}
}

func TestPostgreSQLRestoreRunsBeforeStartupAndElection(t *testing.T) {
	replicas, port, verify := int32(1), int32(5432), false
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "restored", Namespace: "kdb"}, Spec: v1.KDBInstanceSpec{
		Engine: naming.PostgresEngine, EngineVersion: "17", Port: &port,
		InstanceSet: shared.InstanceSetSpec{Replicas: &replicas, MainContainer: shared.ContainerSpec{Image: "postgresql17:test"}, SidecarContainer: shared.ContainerSpec{Image: "postgresql-sidecar:test"}},
		PostgreSQL:  &v1.PostgreSQLSpec{Backups: &v1.PostgreSQLBackupSpec{PGBackRest: &v1.PostgreSQLPGBackRestSpec{Enabled: true, RepoType: "s3", RepoSecretRef: &corev1.LocalObjectReference{Name: "repo"}, S3Bucket: "backups", S3Endpoint: "minio:9000", S3TLSVerify: &verify}}, Restore: &v1.PostgreSQLRestoreBootstrapSpec{OperationID: "restore-1", BackupID: "20260715F", TargetType: "time", Target: "2026-07-15T10:00:00Z", TargetAction: "promote"}},
	}}
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: instance.Name, Namespace: instance.Namespace}}
	postgresPodIntent(newPostgreSQLPodTestContext(t, instance), statefulSet)
	if len(statefulSet.Spec.Template.Spec.InitContainers) != 2 || statefulSet.Spec.Template.Spec.InitContainers[0].Name != "pgbackrest-restore" || statefulSet.Spec.Template.Spec.InitContainers[1].Name != "postgres-startup" {
		t.Fatalf("restore/startup order = %#v", statefulSet.Spec.Template.Spec.InitContainers)
	}
	restore := statefulSet.Spec.Template.Spec.InitContainers[0]
	joined := strings.Join(restore.Args, " ")
	if !strings.Contains(joined, "--set=20260715F") || !strings.Contains(joined, "--type=time") || !strings.Contains(joined, "restore-mode") {
		t.Fatalf("restore args = %q", joined)
	}
	if !hasEnvFromSecret(restore.Env, "PGBACKREST_REPO1_S3_KEY", "repo") {
		t.Fatalf("S3 credential env is missing: %#v", restore.Env)
	}
}

func TestPostgreSQLSameImageInstanceKeepsLegacyRuntimeUntilMigrated(t *testing.T) {
	replicas, port := int32(1), int32(5432)
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "kdb"}, Spec: v1.KDBInstanceSpec{
		Engine: naming.PostgresEngine, EngineVersion: "17", Port: &port,
		InstanceSet: shared.InstanceSetSpec{Replicas: &replicas, MainContainer: shared.ContainerSpec{Image: "postgresql17:legacy"}, SidecarContainer: shared.ContainerSpec{Image: "postgresql17:legacy"}},
	}}
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: instance.Name, Namespace: instance.Namespace}}
	postgresPodIntent(newPostgreSQLPodTestContext(t, instance), statefulSet)
	database := statefulSet.Spec.Template.Spec.Containers[0]
	if strings.Join(database.Command, " ") != "/bin/sh -c exec sleep infinity" || !hasMount(database.VolumeMounts, "patroni-config") {
		t.Fatalf("legacy same-image instance changed before explicit image migration: %#v", database)
	}
}

func newPostgreSQLPodTestContext(t *testing.T, instance *v1.KDBInstance) *reconcilecontext.InstanceContext {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).Build()
	helper := kube.NewDefaultReconcileHelper(kubeClient, nil, nil, scheme)
	base := kube.NewBaseReconcileContext(helper, stdctx.Background(), reconcile.Request{
		NamespacedName: clientKey(instance),
	}, client.FieldOwner("kdb-test"), record.NewFakeRecorder(1))
	rc := reconcilecontext.NewInstanceContext(base)
	if _, err := rc.InitInstance(); err != nil {
		t.Fatal(err)
	}
	return rc
}

func clientKey(instance *v1.KDBInstance) client.ObjectKey {
	return client.ObjectKey{Name: instance.Name, Namespace: instance.Namespace}
}

func hasMount(mounts []corev1.VolumeMount, name string) bool {
	for _, mount := range mounts {
		if mount.Name == name {
			return true
		}
	}
	return false
}

func hasMountPath(mounts []corev1.VolumeMount, path string) bool {
	for _, mount := range mounts {
		if mount.MountPath == path {
			return true
		}
	}
	return false
}

func hasEnvFromSecret(envs []corev1.EnvVar, name, secret string) bool {
	for _, env := range envs {
		if env.Name == name && env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil && env.ValueFrom.SecretKeyRef.Name == secret {
			return true
		}
	}
	return false
}

func hasEnv(envs []corev1.EnvVar, name string) bool {
	for _, env := range envs {
		if env.Name == name {
			return true
		}
	}
	return false
}
