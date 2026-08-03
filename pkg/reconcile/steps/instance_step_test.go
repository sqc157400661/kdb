package steps

import (
	"strings"
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
	for _, raw := range endpoints {
		item, ok := raw.(map[string]interface{})
		if !ok || item["port"] != naming.PortSidecarMetrics {
			continue
		}
		if item["scheme"] != "https" {
			t.Fatalf("expected sidecar metrics endpoint to use https, got %#v", item["scheme"])
		}
		tlsConfig, ok, err := unstructured.NestedMap(item, "tlsConfig")
		if err != nil || !ok {
			t.Fatalf("expected sidecar metrics mTLS config, ok=%v err=%v", ok, err)
		}
		cert, _, _ := unstructured.NestedMap(tlsConfig, "cert", "secret")
		keySecret, _, _ := unstructured.NestedMap(tlsConfig, "keySecret")
		if cert["name"] == nil || keySecret["name"] == nil {
			t.Fatalf("expected client certificate and key references, got %#v", tlsConfig)
		}
		return
	}
	t.Fatal("sidecar metrics endpoint was not found")
}

func TestMySQLPrometheusRuleAlertsOnSQLReadAndWriteProbes(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Name = "demo-mysql"
	instance.Namespace = "kdb"
	instance.Spec.Engine = naming.MySQLEngine
	rule := mysqlPrometheusRule(instance)
	groups, ok, err := unstructured.NestedSlice(rule.Object, "spec", "groups")
	if err != nil || !ok || len(groups) != 1 {
		t.Fatalf("expected one PrometheusRule group, ok=%v err=%v groups=%#v", ok, err, groups)
	}
	group, ok := groups[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected PrometheusRule group: %#v", groups[0])
	}
	rules, ok, err := unstructured.NestedSlice(group, "rules")
	if err != nil || !ok {
		t.Fatalf("expected PrometheusRule rules, ok=%v err=%v", ok, err)
	}
	alerts := map[string]string{}
	for _, raw := range rules {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := item["alert"].(string)
		expr, _ := item["expr"].(string)
		alerts[name] = expr
	}
	for _, name := range []string{"KDBMySQLSQLProbeFailed", "KDBMySQLSQLWriteProbeFailed"} {
		if alerts[name] == "" || !strings.Contains(alerts[name], "== 0") {
			t.Fatalf("missing SQL probe alert %s: %#v", name, alerts)
		}
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
