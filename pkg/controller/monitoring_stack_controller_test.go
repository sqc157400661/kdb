package controller

import (
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestMonitoringStackManifestsRenderExternalLabelsAndSecretBackedRemoteWrite(t *testing.T) {
	stack := &v1.KDBMonitoringStack{}
	stack.Spec.Prometheus.ExternalLabels = map[string]string{
		"cell_id":   "cell-a",
		"tenant_id": "tenant-a",
	}
	stack.Spec.Prometheus.RemoteWrite = []v1.MonitoringStackRemoteWriteSpec{{
		URL: "https://metrics.example.test/api/v1/write",
		AuthorizationSecretRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "metrics-token"},
			Key:                  "token",
		},
	}}
	if err := validateMonitoringStackSpec(stack); err != nil {
		t.Fatalf("validateMonitoringStackSpec() error = %v", err)
	}
	objects := monitoringStackManifests(stack, "kdb-observability")
	var prometheus map[string]any
	for _, object := range objects {
		if object["kind"] != "Prometheus" {
			continue
		}
		var ok bool
		prometheus, ok = object["spec"].(map[string]any)
		if !ok {
			t.Fatalf("Prometheus spec type = %T", object["spec"])
		}
	}
	if prometheus == nil {
		t.Fatal("Prometheus manifest was not rendered")
	}
	labels, ok := prometheus["externalLabels"].(map[string]string)
	if !ok || labels["cell_id"] != "cell-a" || labels["tenant_id"] != "tenant-a" {
		t.Fatalf("external labels = %#v", prometheus["externalLabels"])
	}
	remote, ok := prometheus["remoteWrite"].([]map[string]any)
	if !ok || len(remote) != 1 || remote[0]["url"] != "https://metrics.example.test/api/v1/write" {
		t.Fatalf("remote write = %#v", prometheus["remoteWrite"])
	}
	authorization, ok := remote[0]["authorization"].(map[string]any)
	if !ok || authorization["type"] != "Bearer" {
		t.Fatalf("authorization = %#v", remote[0]["authorization"])
	}
	credentials, ok := authorization["credentials"].(map[string]any)
	if !ok || credentials["name"] != "metrics-token" || credentials["key"] != "token" {
		t.Fatalf("credentials = %#v", authorization["credentials"])
	}
}

func TestMonitoringStackSpecRejectsUnsafeRemoteWriteAndLabels(t *testing.T) {
	unsafeURL := &v1.KDBMonitoringStack{}
	unsafeURL.Spec.Prometheus.RemoteWrite = []v1.MonitoringStackRemoteWriteSpec{{URL: "https://user:pass@metrics.example.test/write"}}
	if err := validateMonitoringStackSpec(unsafeURL); err == nil {
		t.Fatal("remote-write URL with userinfo unexpectedly accepted")
	}
	unsafeLabel := &v1.KDBMonitoringStack{}
	unsafeLabel.Spec.Prometheus.ExternalLabels = map[string]string{"__name__": "not-allowed"}
	if err := validateMonitoringStackSpec(unsafeLabel); err == nil {
		t.Fatal("reserved external label unexpectedly accepted")
	}
}
