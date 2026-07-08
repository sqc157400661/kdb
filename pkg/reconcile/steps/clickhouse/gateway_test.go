package clickhouse

import (
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
)

func TestParseGatewayBindingSecret(t *testing.T) {
	instance := replicatedInstance()
	secret := &corev1.Secret{Data: map[string][]byte{
		gatewayBindingJSONKey: []byte(`{"bindings":[{"account":"bi_service","computeGroup":"serving"}]}`),
		"CHPROXY_PASSWORD_BI_SERVICE": []byte("secret-password"),
	}}

	bindings, err := parseGatewayBindingSecret(secret, instance)
	if err != nil {
		t.Fatalf("parseGatewayBindingSecret() error = %v", err)
	}
	if len(bindings.Bindings) != 1 || bindings.Bindings[0].ComputeGroup != "serving" {
		t.Fatalf("unexpected bindings: %#v", bindings)
	}
}

func TestValidateGatewayBindingsRejectsUnknownGroup(t *testing.T) {
	err := validateGatewayBindings(gatewayBindingSpec{Bindings: []gatewayBinding{{
		Account:      "bi_service",
		ComputeGroup: "missing",
	}}}, replicatedInstance(), &corev1.Secret{Data: map[string][]byte{"CHPROXY_PASSWORD_BI_SERVICE": []byte("secret-password")}})
	if err == nil || !strings.Contains(err.Error(), "unknown compute group") {
		t.Fatalf("expected unknown compute group error, got %v", err)
	}
}

func TestRenderGatewayConfigDoesNotIncludePlaintextPasswords(t *testing.T) {
	instance := replicatedInstance()
	config := renderGatewayConfig(instance, gatewayBindingSpec{Bindings: []gatewayBinding{{
		Account:      "bi_service",
		ComputeGroup: "serving",
	}}})
	if strings.Contains(config, "secret-password") {
		t.Fatalf("gateway config must not contain plaintext password")
	}
	for _, token := range []string{"bi_service", "kdb_serving_group", "${CHPROXY_PASSWORD_BI_SERVICE}"} {
		if !strings.Contains(config, token) {
			t.Fatalf("gateway config missing %q:\n%s", token, config)
		}
	}
}

func TestBuildGatewayResources(t *testing.T) {
	instance := replicatedInstance()
	instance.Spec.ClickHouse.Gateway = &v1.ClickHouseGatewaySpec{Enabled: chTestBool(true)}

	cm := buildGatewayConfigMap(instance, gatewayBindingSpec{Bindings: []gatewayBinding{{
		Account:      "bi_service",
		ComputeGroup: "serving",
	}}})
	if cm.Name != naming.ClickHouseGatewayConfigMapName(instance.Name) || cm.Data[gatewayConfigKey] == "" {
		t.Fatalf("unexpected gateway configmap: %#v", cm)
	}
	deployment := buildGatewayDeployment(instance, "gateway-secret", "revision")
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 2 {
		t.Fatalf("expected two gateway replicas, got %#v", deployment.Spec.Replicas)
	}
	if deployment.Spec.Template.Spec.Containers[0].ReadinessProbe == nil {
		t.Fatalf("gateway deployment should define readiness probe")
	}
	service := buildGatewayService(instance)
	if service.Name != naming.ClickHouseGatewayServiceName(instance.Name) || len(service.Spec.Ports) != 2 {
		t.Fatalf("unexpected gateway service: %#v", service)
	}
}

func chTestBool(v bool) *bool {
	return &v
}
