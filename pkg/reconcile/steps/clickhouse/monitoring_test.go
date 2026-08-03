package clickhouse

import (
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestClickHouseMonitoringObjectsExposeMTLSSidecarContract(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Name = "orders"
	instance.Namespace = "kdb"
	instance.Spec.ClickHouse = &v1.ClickHouseSpec{}

	objects := clickHouseMonitoringObjects(instance)
	if len(objects) != 2 {
		t.Fatalf("monitoring objects = %d, want PodMonitor and PrometheusRule", len(objects))
	}
	podMonitor := objects[0]
	if podMonitor.GetKind() != "PodMonitor" {
		t.Fatalf("first object kind = %q", podMonitor.GetKind())
	}
	selector, found, err := unstructured.NestedMap(podMonitor.Object, "spec", "selector", "matchLabels")
	if err != nil || !found {
		t.Fatalf("PodMonitor selector missing: found=%v err=%v", found, err)
	}
	if selector[naming.LabelClickHouseComponent] != naming.ClickHouseComponentClickHouse {
		t.Fatalf("selector component = %v", selector[naming.LabelClickHouseComponent])
	}
	endpoints, found, err := unstructured.NestedSlice(podMonitor.Object, "spec", "podMetricsEndpoints")
	if err != nil || !found || len(endpoints) != 1 {
		t.Fatalf("PodMonitor endpoints = %#v, found=%v err=%v", endpoints, found, err)
	}
	endpoint, ok := endpoints[0].(map[string]interface{})
	if !ok || endpoint["scheme"] != "https" || endpoint["port"] != naming.PortSidecarMetrics {
		t.Fatalf("endpoint = %#v", endpoint)
	}
	tls, found, err := unstructured.NestedMap(endpoint, "tlsConfig")
	if err != nil || !found {
		t.Fatalf("TLS config missing: found=%v err=%v", found, err)
	}
	ca, found, err := unstructured.NestedMap(tls, "ca", "secret")
	if err != nil || !found || ca["name"] != naming.ClickHouseSecretName(instance.Name) || ca["key"] != naming.ClickHouseTLSCAKey {
		t.Fatalf("TLS CA = %#v, found=%v err=%v", ca, found, err)
	}
	rule := objects[1]
	if rule.GetKind() != "PrometheusRule" {
		t.Fatalf("second object kind = %q", rule.GetKind())
	}
	groups, found, err := unstructured.NestedSlice(rule.Object, "spec", "groups")
	if err != nil || !found || len(groups) == 0 {
		t.Fatal("PrometheusRule has no rules")
	}
	group, ok := groups[0].(map[string]interface{})
	if !ok {
		t.Fatalf("PrometheusRule group = %#v", groups[0])
	}
	rules, found, err := unstructured.NestedSlice(group, "rules")
	if err != nil || !found || len(rules) == 0 {
		t.Fatal("PrometheusRule group has no rules")
	}
}
