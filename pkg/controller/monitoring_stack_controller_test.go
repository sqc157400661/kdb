package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

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

func TestMonitoringStackRelayIsExplicitSecretBackedAndRoutesAlertmanager(t *testing.T) {
	enabled := true
	stack := &v1.KDBMonitoringStack{}
	stack.Name = "primary"
	stack.Spec.Relay = v1.MonitoringStackAlertRelaySpec{
		Enabled: &enabled, Image: "registry.example.test/kdb/operator@sha256:relay", CellID: "cell-a",
		CenterEndpointSecretRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "alert-center"}, Key: "endpoint"},
		TLSSecretRef:            &corev1.SecretReference{Name: "alert-relay-tls"},
	}
	if err := validateMonitoringStackSpec(stack); err != nil {
		t.Fatal(err)
	}
	objects := monitoringStackManifests(stack, "kdb-observability")
	kinds := map[string]map[string]any{}
	for _, object := range objects {
		if metadata, ok := object["metadata"].(map[string]any); ok && metadata["name"] == alertRelayService {
			kinds[object["kind"].(string)] = object
		}
	}
	for _, kind := range []string{"Deployment", "Service", "ServiceMonitor", "PrometheusRule", "AlertmanagerConfig", "NetworkPolicy"} {
		if kinds[kind] == nil {
			t.Fatalf("relay %s manifest missing", kind)
		}
	}
	deploymentJSON := mustJSON(t, kinds["Deployment"])
	for _, want := range []string{"KDB_ALERT_CENTER_ENDPOINT", "secretKeyRef", "alert-relay-tls", "/kdb/bin/alert-relay"} {
		if !strings.Contains(deploymentJSON, want) {
			t.Errorf("relay Deployment missing %q: %s", want, deploymentJSON)
		}
	}
	if strings.Contains(deploymentJSON, "fallbackSecretRef") {
		t.Fatal("T05 Relay must not consume the T10 fallback Secret")
	}
	configJSON := mustJSON(t, kinds["AlertmanagerConfig"])
	if !strings.Contains(configJSON, "/v1/alertmanager") || !strings.Contains(configJSON, `"sendResolved":true`) {
		t.Fatalf("AlertmanagerConfig=%s", configJSON)
	}

	disabled := &v1.KDBMonitoringStack{}
	for _, object := range monitoringStackManifests(disabled, "kdb-observability") {
		if metadata, ok := object["metadata"].(map[string]any); ok && metadata["name"] == alertRelayService && object["kind"] == "Deployment" {
			t.Fatal("Relay Deployment rendered without explicit enablement")
		}
	}
}

func TestMonitoringStackRelayConsumesFallbackOnlyThroughSecretFile(t *testing.T) {
	enabled := true
	stack := &v1.KDBMonitoringStack{}
	stack.Name = "primary"
	stack.Spec.Relay = v1.MonitoringStackAlertRelaySpec{
		Enabled: &enabled, Image: "relay@sha256:test", CellID: "cell-a",
		CenterEndpointSecretRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "center"}, Key: "endpoint"},
		TLSSecretRef:            &corev1.SecretReference{Name: "relay-tls"},
		FallbackSecretRef:       &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "cell-emergency"}, Key: "fallback.json"},
	}
	if err := validateMonitoringStackSpec(stack); err != nil {
		t.Fatal(err)
	}
	objects := monitoringAlertRelayManifests(stack, "kdb-observability")
	deployment := mustJSON(t, objects[0])
	for _, want := range []string{"KDB_ALERT_FALLBACK_CONFIG", "/var/run/kdb-alert-relay/fallback/fallback.json", "cell-emergency", "relay-state", "/var/lib/kdb-alert-relay", "persistentVolumeClaim", alertRelayStateClaim} {
		if !strings.Contains(deployment, want) {
			t.Errorf("fallback projection missing %q: %s", want, deployment)
		}
	}
	if strings.Contains(deployment, "https://") || strings.Contains(deployment, "Authorization") {
		t.Fatalf("raw fallback endpoint or credential leaked: %s", deployment)
	}
	if got := mustJSON(t, objects[len(objects)-1]); !strings.Contains(got, `"kind":"PersistentVolumeClaim"`) || !strings.Contains(got, `"storage":"1Gi"`) {
		t.Fatalf("fallback state PVC=%s", got)
	}
}

func TestMonitoringStackRelayRejectsMissingOrCrossNamespaceSecrets(t *testing.T) {
	enabled := true
	stack := &v1.KDBMonitoringStack{}
	stack.Spec.Relay = v1.MonitoringStackAlertRelaySpec{Enabled: &enabled, Image: "relay:v1", CellID: "cell-a"}
	if err := validateMonitoringStackSpec(stack); err == nil {
		t.Fatal("enabled Relay without Secret references accepted")
	}
	stack.Spec.Relay.CenterEndpointSecretRef = &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "center"}, Key: "endpoint"}
	stack.Spec.Relay.TLSSecretRef = &corev1.SecretReference{Name: "tls", Namespace: "other"}
	if err := validateMonitoringStackSpec(stack); err == nil {
		t.Fatal("cross-namespace Relay TLS Secret accepted")
	}
}

func TestDisabledMonitoringRelayPrunesOnlyManagedExactObjects(t *testing.T) {
	namespace := "kdb-observability"
	objects := []runtime.Object{}
	for _, identity := range []struct{ apiVersion, kind, name string }{
		{apiVersion: "apps/v1", kind: "Deployment", name: alertRelayDeployment},
		{apiVersion: "v1", kind: "Service", name: alertRelayService},
		{apiVersion: "monitoring.coreos.com/v1alpha1", kind: "AlertmanagerConfig", name: alertRelayService},
		{apiVersion: "networking.k8s.io/v1", kind: "NetworkPolicy", name: alertRelayService},
	} {
		objects = append(objects, &unstructured.Unstructured{Object: map[string]any{"apiVersion": identity.apiVersion, "kind": identity.kind, "metadata": map[string]any{"name": identity.name, "namespace": namespace, "labels": map[string]any{"app.kubernetes.io/managed-by": "kdb-operator"}}}})
	}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
	if err := removeDisabledMonitoringRelay(context.Background(), client, namespace); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []struct{ kind, name string }{{"Deployment", alertRelayDeployment}, {"Service", alertRelayService}, {"AlertmanagerConfig", alertRelayService}, {"NetworkPolicy", alertRelayService}} {
		gvr, _ := monitoringGVRForKind(identity.kind)
		if _, err := client.Resource(gvr).Namespace(namespace).Get(context.Background(), identity.name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("managed %s was not pruned: %v", identity.kind, err)
		}
	}

	unmanaged := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": alertRelayDeployment, "namespace": namespace}}}
	client = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), unmanaged)
	if err := removeDisabledMonitoringRelay(context.Background(), client, namespace); err == nil {
		t.Fatal("disabled Relay cleanup deleted or accepted an unmanaged reserved object")
	}
}
