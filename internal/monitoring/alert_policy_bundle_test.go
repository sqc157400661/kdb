package monitoring

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestResolveAlertPolicyBundleStateMachine(t *testing.T) {
	if AlertPolicyConfigMapName("mysql-orders") != "kdb-alert-policy-e2816723fcb6" {
		t.Fatalf("unexpected ConfigMap name %q", AlertPolicyConfigMapName("mysql-orders"))
	}
	instance := alertPolicyTestInstance()
	generated := alertPolicyTestRule(false, 0)
	existing := alertPolicyTestRule(true, 6)

	prepared := alertPolicyTestConfigMap(t, instance, AlertPolicyModePrepared, 7, []string{"mysql_up"})
	decision := ResolveAlertPolicyBundle(instance, prepared, generated, existing, true)
	if decision.Apply || decision.Status != AlertPolicyModePrepared || decision.Rule.GetAnnotations()[AlertPolicyRevisionAnnotation] != "6" {
		t.Fatalf("prepared decision = %#v", decision)
	}

	active := alertPolicyTestConfigMap(t, instance, AlertPolicyModeActive, 7, []string{"mysql_up"})
	decision = ResolveAlertPolicyBundle(instance, active, generated, existing, true)
	if !decision.Apply || decision.Status != AlertPolicyModeActive {
		t.Fatalf("active decision = %#v", decision)
	}
	groups, _, _ := unstructured.NestedSlice(decision.Rule.Object, "spec", "groups")
	if len(groups) != 2 {
		t.Fatalf("active groups = %d, want recording and policy groups", len(groups))
	}
	if got := alertNames(decision.Rule); len(got) != 1 || got[0] != "KDBPolicyTest" {
		t.Fatalf("active alerts = %v", got)
	}

	disabled := alertPolicyTestConfigMap(t, instance, AlertPolicyModeDisabled, 8, []string{"mysql_up"})
	decision = ResolveAlertPolicyBundle(instance, disabled, generated, existing, true)
	if !decision.Apply || decision.Status != AlertPolicyModeDisabled || len(alertNames(decision.Rule)) != 0 {
		t.Fatalf("disabled decision = %#v alerts=%v", decision, alertNames(decision.Rule))
	}
}

func TestResolveAlertPolicyBundleRejectsInvalidAndRetainsLastActive(t *testing.T) {
	instance := alertPolicyTestInstance()
	generated := alertPolicyTestRule(false, 0)
	existing := alertPolicyTestRule(true, 9)

	tests := []struct {
		name  string
		cm    *corev1.ConfigMap
		ready bool
	}{
		{name: "revision regression", cm: alertPolicyTestConfigMap(t, instance, AlertPolicyModeActive, 8, []string{"mysql_up"}), ready: true},
		{name: "monitoring unavailable", cm: alertPolicyTestConfigMap(t, instance, AlertPolicyModeActive, 10, []string{"mysql_up"}), ready: false},
		{name: "metric unavailable", cm: alertPolicyTestConfigMap(t, instance, AlertPolicyModeActive, 10, []string{"unknown_metric"}), ready: true},
	}
	badChecksum := alertPolicyTestConfigMap(t, instance, AlertPolicyModeActive, 10, []string{"mysql_up"})
	badChecksum.Data[AlertPolicyBundleDataKey] += " "
	var payload map[string]interface{}
	_ = json.Unmarshal([]byte(badChecksum.Data[AlertPolicyBundleDataKey]), &payload)
	payload["spec"].(map[string]interface{})["mode"] = AlertPolicyModeDisabled
	changed, _ := json.Marshal(payload)
	badChecksum.Data[AlertPolicyBundleDataKey] = string(changed)
	tests = append(tests, struct {
		name  string
		cm    *corev1.ConfigMap
		ready bool
	}{name: "checksum mismatch", cm: badChecksum, ready: true})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := ResolveAlertPolicyBundle(instance, test.cm, generated, existing, test.ready)
			if decision.Apply || decision.Status != AlertPolicyStatusInvalid || decision.Rule.GetAnnotations()[AlertPolicyRevisionAnnotation] != "9" {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func alertPolicyTestInstance() *v1.KDBInstance {
	return &v1.KDBInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "database", Labels: map[string]string{
			naming.LabelScopeTenant: "tenant-a", naming.LabelScopeProject: "project-a",
			naming.LabelScopeEnvironment: "prod", naming.LabelScopeRegion: "cn-north",
			naming.LabelScopeInstance: "instance-a",
		}},
		Spec: v1.KDBInstanceSpec{Engine: "mysql"},
	}
}

func alertPolicyTestConfigMap(t *testing.T, instance *v1.KDBInstance, mode string, revision int64, required []string) *corev1.ConfigMap {
	t.Helper()
	bundle := AlertPolicyBundle{
		APIVersion: "alerting.kdb.io/v1", Kind: "AlertPolicyBundle",
		Metadata: AlertPolicyBundleMetadata{InstanceID: platformInstanceID(instance), Namespace: instance.Namespace, Engine: "mysql", PolicyRevision: revision},
		Spec: AlertPolicyBundleSpec{Mode: mode, RequiredMetrics: required, Groups: []AlertPolicyBundleGroup{{
			Name: "kdb.policy", Rules: []AlertPolicyBundleRule{{
				Alert: "KDBPolicyTest",
				Expr:  `mysql_up{tenant_id="tenant-a",project_id="project-a",environment_id="prod",region_id="cn-north",instance_id="instance-a"} == 0`,
				For:   "2m", Labels: map[string]string{"kdb_template_key": "mysql.up", "kdb_template_version": "1", "kdb_policy_id": "42", "kdb_policy_revision": "7"},
				Annotations: map[string]string{"summary": "test"},
			}},
		}}},
	}
	canonical, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	bundle.Metadata.Checksum = "sha256:" + hex.EncodeToString(sum[:])
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: AlertPolicyConfigMapName(platformInstanceID(instance)), Namespace: instance.Namespace, Labels: map[string]string{
		AlertPolicyManagedLabel: "true", AlertPolicyInstanceLabel: platformInstanceID(instance),
	}}, Data: map[string]string{AlertPolicyBundleDataKey: string(raw)}}
}

func alertPolicyTestRule(active bool, revision int64) *unstructured.Unstructured {
	annotations := map[string]string{}
	if active {
		annotations[AlertPolicyActiveAnnotation] = "true"
		annotations[AlertPolicyRevisionAnnotation] = strconv.FormatInt(revision, 10)
	}
	result := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "monitoring.coreos.com/v1", "kind": "PrometheusRule",
		"metadata": map[string]interface{}{"name": "orders-mysql", "namespace": "database"},
		"spec": map[string]interface{}{"groups": []interface{}{map[string]interface{}{"name": "legacy", "rules": []interface{}{
			map[string]interface{}{"record": "kdb_record", "expr": "vector(1)"},
			map[string]interface{}{"alert": "KDBLegacy", "expr": "vector(0)"},
		}}}},
	}}
	result.SetAnnotations(annotations)
	return result
}

func alertNames(rule *unstructured.Unstructured) []string {
	groups, _, _ := unstructured.NestedSlice(rule.Object, "spec", "groups")
	result := []string{}
	for _, rawGroup := range groups {
		group, _ := rawGroup.(map[string]interface{})
		rules, _, _ := unstructured.NestedSlice(group, "rules")
		for _, rawRule := range rules {
			item, _ := rawRule.(map[string]interface{})
			if name, _ := item["alert"].(string); name != "" {
				result = append(result, name)
			}
		}
	}
	return result
}
