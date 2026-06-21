package steps

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/util"
)

func TestStoppedInstanceStatefulSetNamesKeepsDesiredSets(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Name = "demo"
	instance.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: util.Int32(2)}

	names := stoppedInstanceStatefulSetNames(instance)

	if !names.Has("demo0") || !names.Has("demo1") {
		t.Fatalf("expected stopped desired StatefulSets to be kept, got %v", names.List())
	}
	if names.Has("demo2") {
		t.Fatalf("expected extra StatefulSet demo2 not to be kept")
	}
}

func TestMySQLPodMonitorScrapesExporterAndSidecar(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Name = "demo-mysql"
	instance.Namespace = "kdb"
	instance.Spec.Engine = naming.MySQLEngine
	instance.Spec.MySQL = &v1.MySQLSpec{Exporter: &v1.MySQLExporterSpec{Enabled: true}}

	if !mysqlMonitoringEnabled(instance) {
		t.Fatalf("expected mysql monitoring to be enabled")
	}
	obj := mysqlPodMonitor(instance)
	endpoints, ok, err := unstructured.NestedSlice(obj.Object, "spec", "podMetricsEndpoints")
	if err != nil || !ok {
		t.Fatalf("expected pod monitor endpoints, ok=%v err=%v", ok, err)
	}
	ports := map[string]bool{}
	for _, endpoint := range endpoints {
		item, ok := endpoint.(map[string]interface{})
		if !ok {
			t.Fatalf("unexpected endpoint: %#v", endpoint)
		}
		if port, _ := item["port"].(string); port != "" {
			ports[port] = true
		}
	}
	if !ports[naming.PortMySQLMetrics] || !ports[naming.PortSidecarMetrics] {
		t.Fatalf("expected exporter and sidecar metrics ports, got %#v", ports)
	}
}

func TestPostgresCredentialEnvUsesGeneratedSecretName(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Name = "demo-pg"

	env := postgresCredentialEnv(instance, "PATRONI_POSTGRESQL_AUTHENTICATION_SUPERUSER_PASSWORD", naming.PostgreSQLSuperuserPasswordKey)

	if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected secret key ref env, got %#v", env)
	}
	if got := env.ValueFrom.SecretKeyRef.Name; got != "demo-pg-postgresql-credential" {
		t.Fatalf("unexpected secret name: %s", got)
	}
	if got := env.ValueFrom.SecretKeyRef.Key; got != naming.PostgreSQLSuperuserPasswordKey {
		t.Fatalf("unexpected secret key: %s", got)
	}
}

func TestPostgresCredentialEnvUsesCustomSecretRef(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Name = "demo-pg"
	instance.Spec.PostgreSQL = &v1.PostgreSQLSpec{
		CredentialSecretRef: &corev1.LocalObjectReference{Name: "custom-pg-secret"},
	}

	env := postgresCredentialEnv(instance, "PATRONI_POSTGRESQL_AUTHENTICATION_REPLICATION_PASSWORD", naming.PostgreSQLReplicationPasswordKey)

	if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected secret key ref env, got %#v", env)
	}
	if got := env.ValueFrom.SecretKeyRef.Name; got != "custom-pg-secret" {
		t.Fatalf("unexpected secret name: %s", got)
	}
	if got := env.ValueFrom.SecretKeyRef.Key; got != naming.PostgreSQLReplicationPasswordKey {
		t.Fatalf("unexpected secret key: %s", got)
	}
}
