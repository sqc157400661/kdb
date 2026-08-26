package monitoring

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	AlertPolicyManagedLabel       = "kdb.io/managed-alert-policy"
	AlertPolicyInstanceLabel      = "kdb.io/alert-policy-instance"
	AlertPolicyBundleDataKey      = "bundle.yaml"
	AlertPolicyStatusAnnotation   = "kdb.io/alert-policy-status"
	AlertPolicyReasonAnnotation   = "kdb.io/alert-policy-reason"
	AlertPolicyRevisionAnnotation = "kdb.io/alert-policy-revision"
	AlertPolicyChecksumAnnotation = "kdb.io/alert-policy-checksum"
	AlertPolicyActiveAnnotation   = "kdb.io/alert-policy-active"

	AlertPolicyModePrepared  = "Prepared"
	AlertPolicyModeActive    = "Active"
	AlertPolicyModeDisabled  = "Disabled"
	AlertPolicyStatusInvalid = "Invalid"
)

var ErrAlertPolicyBundleInvalid = errors.New("alert policy bundle invalid")

type AlertPolicyBundle struct {
	APIVersion string                    `json:"apiVersion"`
	Kind       string                    `json:"kind"`
	Metadata   AlertPolicyBundleMetadata `json:"metadata"`
	Spec       AlertPolicyBundleSpec     `json:"spec"`
}

type AlertPolicyBundleMetadata struct {
	InstanceID     string `json:"instanceId"`
	Namespace      string `json:"namespace,omitempty"`
	Engine         string `json:"engine"`
	PolicyRevision int64  `json:"policyRevision"`
	Checksum       string `json:"checksum,omitempty"`
}

type AlertPolicyBundleSpec struct {
	Mode            string                   `json:"mode"`
	RequiredMetrics []string                 `json:"requiredMetrics,omitempty"`
	Groups          []AlertPolicyBundleGroup `json:"groups"`
}

type AlertPolicyBundleGroup struct {
	Name  string                  `json:"name"`
	Rules []AlertPolicyBundleRule `json:"rules"`
}

type AlertPolicyBundleRule struct {
	Alert       string            `json:"alert"`
	Expr        string            `json:"expr"`
	For         string            `json:"for,omitempty"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

type AlertPolicyDecision struct {
	Rule          *unstructured.Unstructured
	Apply         bool
	Status        string
	Reason        string
	Revision      int64
	Checksum      string
	ConfigMapName string
}

func AlertPolicyConfigMapName(instanceID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(instanceID)))
	return "kdb-alert-policy-" + hex.EncodeToString(sum[:6])
}

func ResolveAlertPolicyBundle(
	instance *v1.KDBInstance,
	configMap *corev1.ConfigMap,
	generated,
	existing *unstructured.Unstructured,
	monitoringReady bool,
) AlertPolicyDecision {
	legacy := func() AlertPolicyDecision {
		if isActivePolicyRule(existing) {
			return AlertPolicyDecision{Rule: existing.DeepCopy(), Apply: false, Status: AlertPolicyModeActive, Reason: "LastActiveRetained"}
		}
		return AlertPolicyDecision{Rule: generated.DeepCopy(), Apply: true, Status: "Legacy", Reason: "BundleAbsent"}
	}
	if instance == nil || generated == nil {
		return AlertPolicyDecision{Status: AlertPolicyStatusInvalid, Reason: "TargetUnavailable"}
	}
	if configMap == nil {
		return legacy()
	}
	decision := AlertPolicyDecision{ConfigMapName: configMap.Name}
	bundle, err := decodeAndValidateAlertPolicyBundle(instance, configMap)
	if err != nil {
		return retainPolicyRule(decision, generated, existing, err.Error())
	}
	decision.Revision = bundle.Metadata.PolicyRevision
	decision.Checksum = bundle.Metadata.Checksum
	if activeRevision(existing) > bundle.Metadata.PolicyRevision {
		return retainPolicyRule(decision, generated, existing, "RevisionRegression")
	}
	switch bundle.Spec.Mode {
	case AlertPolicyModePrepared:
		decision.Status = AlertPolicyModePrepared
		decision.Reason = "ValidatedWithoutActivation"
		if existing != nil {
			decision.Rule = existing.DeepCopy()
			return decision
		}
		decision.Rule = generated.DeepCopy()
		decision.Apply = true
		return decision
	case AlertPolicyModeActive, AlertPolicyModeDisabled:
		if !monitoringReady {
			return retainPolicyRule(decision, generated, existing, "MonitoringStackNotReady")
		}
		if missing := missingRequiredMetrics(bundle.Metadata.Engine, bundle.Spec.RequiredMetrics); len(missing) > 0 {
			return retainPolicyRule(decision, generated, existing, "RequiredMetricUnavailable:"+strings.Join(missing, ","))
		}
		projected, projectErr := projectAlertPolicyRules(generated, bundle)
		if projectErr != nil {
			return retainPolicyRule(decision, generated, existing, projectErr.Error())
		}
		decision.Rule = projected
		decision.Apply = true
		decision.Status = bundle.Spec.Mode
		decision.Reason = "Validated"
		return decision
	default:
		return retainPolicyRule(decision, generated, existing, "UnsupportedMode")
	}
}

func ResolveAlertPolicyForInstance(ctx context.Context, reader client.Client, instance *v1.KDBInstance, generated *unstructured.Unstructured) (AlertPolicyDecision, error) {
	if reader == nil || instance == nil || generated == nil {
		return AlertPolicyDecision{}, ErrAlertPolicyBundleInvalid
	}
	configMap := &corev1.ConfigMap{}
	name := AlertPolicyConfigMapName(platformInstanceID(instance))
	err := reader.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: name}, configMap)
	if apierrors.IsNotFound(err) {
		configMap = nil
	} else if err != nil {
		return AlertPolicyDecision{}, err
	}
	existing := generated.DeepCopy()
	err = reader.Get(ctx, client.ObjectKeyFromObject(generated), existing)
	if apierrors.IsNotFound(err) {
		existing = nil
	} else if err != nil {
		return AlertPolicyDecision{}, err
	}
	ready := false
	if configMap != nil {
		ready, err = monitoringStackReady(ctx, reader)
		if err != nil {
			return AlertPolicyDecision{}, err
		}
	}
	decision := ResolveAlertPolicyBundle(instance, configMap, generated, existing, ready)
	if configMap != nil && (!decision.Apply || decision.Status == AlertPolicyStatusInvalid || decision.Status == AlertPolicyModePrepared) {
		if err := PatchAlertPolicyBundleStatus(ctx, reader, instance, configMap, decision); err != nil {
			return AlertPolicyDecision{}, err
		}
	}
	return decision, nil
}

func PatchAlertPolicyBundleStatus(ctx context.Context, writer client.Client, instance *v1.KDBInstance, configMap *corev1.ConfigMap, decision AlertPolicyDecision) error {
	if writer == nil || instance == nil || configMap == nil {
		return ErrAlertPolicyBundleInvalid
	}
	before := configMap.DeepCopy()
	annotations := configMap.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AlertPolicyStatusAnnotation] = decision.Status
	annotations[AlertPolicyReasonAnnotation] = boundedReason(decision.Reason)
	annotations[AlertPolicyRevisionAnnotation] = strconv.FormatInt(decision.Revision, 10)
	annotations[AlertPolicyChecksumAnnotation] = decision.Checksum
	configMap.SetAnnotations(annotations)
	if !metav1.IsControlledBy(configMap, instance) {
		if controller := metav1.GetControllerOf(configMap); controller != nil {
			return fmt.Errorf("%w: configmap has another controller", ErrAlertPolicyBundleInvalid)
		}
		configMap.SetOwnerReferences(append(configMap.GetOwnerReferences(), *metav1.NewControllerRef(instance, v1.GroupVersion.WithKind(v1.KDBInstanceKindName))))
	}
	if equalStatusProjection(before, configMap) {
		return nil
	}
	return writer.Patch(ctx, configMap, client.MergeFrom(before))
}

func MarkAlertPolicyBundleApplied(ctx context.Context, writer client.Client, instance *v1.KDBInstance, decision AlertPolicyDecision) error {
	if decision.ConfigMapName == "" {
		return nil
	}
	configMap := &corev1.ConfigMap{}
	if err := writer.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: decision.ConfigMapName}, configMap); err != nil {
		return err
	}
	return PatchAlertPolicyBundleStatus(ctx, writer, instance, configMap, decision)
}

func decodeAndValidateAlertPolicyBundle(instance *v1.KDBInstance, configMap *corev1.ConfigMap) (AlertPolicyBundle, error) {
	if configMap.Labels[AlertPolicyManagedLabel] != "true" || configMap.Labels[AlertPolicyInstanceLabel] != platformInstanceID(instance) {
		return AlertPolicyBundle{}, fmt.Errorf("%w: target labels", ErrAlertPolicyBundleInvalid)
	}
	raw := strings.TrimSpace(configMap.Data[AlertPolicyBundleDataKey])
	if raw == "" || len(raw) > 1<<20 {
		return AlertPolicyBundle{}, fmt.Errorf("%w: payload", ErrAlertPolicyBundleInvalid)
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var bundle AlertPolicyBundle
	if err := decoder.Decode(&bundle); err != nil {
		return AlertPolicyBundle{}, fmt.Errorf("%w: schema", ErrAlertPolicyBundleInvalid)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return AlertPolicyBundle{}, fmt.Errorf("%w: trailing payload", ErrAlertPolicyBundleInvalid)
	}
	if bundle.APIVersion != "alerting.kdb.io/v1" || bundle.Kind != "AlertPolicyBundle" ||
		bundle.Metadata.InstanceID != platformInstanceID(instance) ||
		(bundle.Metadata.Namespace != "" && bundle.Metadata.Namespace != instance.Namespace) ||
		!strings.EqualFold(bundle.Metadata.Engine, naming.Engine(instance)) || bundle.Metadata.PolicyRevision <= 0 ||
		bundle.Metadata.Checksum == "" || len(bundle.Spec.Groups) > 100 {
		return AlertPolicyBundle{}, fmt.Errorf("%w: identity", ErrAlertPolicyBundleInvalid)
	}
	checksum := bundle.Metadata.Checksum
	bundle.Metadata.Checksum = ""
	canonical, err := json.Marshal(bundle)
	if err != nil {
		return AlertPolicyBundle{}, err
	}
	sum := sha256.Sum256(canonical)
	if checksum != "sha256:"+hex.EncodeToString(sum[:]) {
		return AlertPolicyBundle{}, fmt.Errorf("%w: checksum", ErrAlertPolicyBundleInvalid)
	}
	bundle.Metadata.Checksum = checksum
	for _, group := range bundle.Spec.Groups {
		if strings.TrimSpace(group.Name) == "" || len(group.Rules) > 1000 {
			return AlertPolicyBundle{}, fmt.Errorf("%w: group", ErrAlertPolicyBundleInvalid)
		}
		for _, rule := range group.Rules {
			if strings.TrimSpace(rule.Alert) == "" || strings.TrimSpace(rule.Expr) == "" ||
				rule.Labels["kdb_template_key"] == "" || rule.Labels["kdb_template_version"] == "" ||
				rule.Labels["kdb_policy_id"] == "" || rule.Labels["kdb_policy_revision"] == "" ||
				!expressionHasTargetMatchers(rule.Expr, instance) {
				return AlertPolicyBundle{}, fmt.Errorf("%w: rule", ErrAlertPolicyBundleInvalid)
			}
		}
	}
	return bundle, nil
}

func projectAlertPolicyRules(generated *unstructured.Unstructured, bundle AlertPolicyBundle) (*unstructured.Unstructured, error) {
	result := generated.DeepCopy()
	generatedGroups, found, err := unstructured.NestedSlice(generated.Object, "spec", "groups")
	if err != nil || !found {
		return nil, ErrAlertPolicyBundleInvalid
	}
	groups := make([]interface{}, 0, len(bundle.Spec.Groups)+1)
	for _, rawGroup := range generatedGroups {
		group, ok := rawGroup.(map[string]interface{})
		if !ok {
			return nil, ErrAlertPolicyBundleInvalid
		}
		rawRules, _, _ := unstructured.NestedSlice(group, "rules")
		recording := make([]interface{}, 0, len(rawRules))
		for _, rawRule := range rawRules {
			rule, ok := rawRule.(map[string]interface{})
			record, hasRecord := rule["record"].(string)
			if ok && hasRecord && strings.TrimSpace(record) != "" {
				recording = append(recording, rule)
			}
		}
		if len(recording) > 0 {
			groups = append(groups, map[string]interface{}{"name": fmt.Sprint(group["name"]), "rules": recording})
		}
	}
	if bundle.Spec.Mode == AlertPolicyModeActive {
		for _, bundleGroup := range bundle.Spec.Groups {
			rules := make([]interface{}, 0, len(bundleGroup.Rules))
			for _, bundleRule := range bundleGroup.Rules {
				rules = append(rules, map[string]interface{}{
					"alert": bundleRule.Alert, "expr": bundleRule.Expr, "for": bundleRule.For,
					"labels": stringInterfaceMap(bundleRule.Labels), "annotations": stringInterfaceMap(bundleRule.Annotations),
				})
			}
			groups = append(groups, map[string]interface{}{"name": bundleGroup.Name, "rules": rules})
		}
	}
	if err := unstructured.SetNestedSlice(result.Object, groups, "spec", "groups"); err != nil {
		return nil, err
	}
	annotations := result.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AlertPolicyActiveAnnotation] = "true"
	annotations[AlertPolicyRevisionAnnotation] = strconv.FormatInt(bundle.Metadata.PolicyRevision, 10)
	annotations[AlertPolicyChecksumAnnotation] = bundle.Metadata.Checksum
	result.SetAnnotations(annotations)
	return result, nil
}

func retainPolicyRule(base AlertPolicyDecision, generated, existing *unstructured.Unstructured, reason string) AlertPolicyDecision {
	base.Status = AlertPolicyStatusInvalid
	base.Reason = boundedReason(reason)
	if existing != nil {
		base.Rule = existing.DeepCopy()
		return base
	}
	base.Rule = generated.DeepCopy()
	base.Apply = true
	return base
}

func isActivePolicyRule(rule *unstructured.Unstructured) bool {
	return rule != nil && rule.GetAnnotations()[AlertPolicyActiveAnnotation] == "true"
}

func activeRevision(rule *unstructured.Unstructured) int64 {
	if !isActivePolicyRule(rule) {
		return 0
	}
	value, _ := strconv.ParseInt(rule.GetAnnotations()[AlertPolicyRevisionAnnotation], 10, 64)
	return value
}

func monitoringStackReady(ctx context.Context, reader client.Client) (bool, error) {
	stacks := &v1.KDBMonitoringStackList{}
	if err := reader.List(ctx, stacks); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	for index := range stacks.Items {
		if stacks.Items[index].Status.Ready && stacks.Items[index].Status.Phase == v1.MonitoringStackPhaseReady {
			return true, nil
		}
	}
	return false, nil
}

func missingRequiredMetrics(engine string, required []string) []string {
	available := currentMetricCapabilities(strings.ToLower(strings.TrimSpace(engine)))
	missing := make([]string, 0)
	for _, metric := range required {
		metric = strings.TrimSpace(metric)
		if metric == "" || !available[metric] {
			missing = append(missing, metric)
		}
	}
	sort.Strings(missing)
	return missing
}

func currentMetricCapabilities(engine string) map[string]bool {
	result := map[string]bool{}
	var metrics []string
	switch engine {
	case "mysql":
		metrics = []string{"mysql_up", "kdb_sidecar_up", "kdb_mysql_mgr_sql_up", "kdb_mysql_mgr_sql_write_up", "kdb_mysql_mgr_replication_lag_seconds"}
	case "postgresql", "pg":
		metrics = []string{"pg_up", "kdb_pg_probe_sql_up", "up", "kdb_ha_postgres_primary", "pg_replication_lag_seconds", "pg_stat_database_numbackends", "kdb_pg_settings_max_connections", "kdb_pg_activity_longest_transaction_seconds", "pg_stat_database_deadlocks", "kdb_pg_database_freeze_age_ratio", "pg_stat_archiver_failed_count", "kdb_pgbackrest_backup_last_success_timestamp_seconds", "kubelet_volume_stats_used_bytes", "kubelet_volume_stats_capacity_bytes"}
	case "clickhouse":
		metrics = []string{"kdb_clickhouse_up", "kdb_clickhouse_sql_up", "kdb_sidecar_up", "kubelet_volume_stats_used_bytes", "kubelet_volume_stats_capacity_bytes"}
	}
	for _, metric := range metrics {
		result[metric] = true
	}
	return result
}

func expressionHasTargetMatchers(expression string, instance *v1.KDBInstance) bool {
	labels := map[string]string{
		"tenant_id": instance.Labels[naming.LabelScopeTenant], "project_id": instance.Labels[naming.LabelScopeProject],
		"environment_id": instance.Labels[naming.LabelScopeEnvironment], "region_id": instance.Labels[naming.LabelScopeRegion],
		"instance_id": platformInstanceID(instance),
	}
	for key, value := range labels {
		if value == "" || !strings.Contains(expression, key+"="+strconv.Quote(value)) {
			return false
		}
	}
	return true
}

func platformInstanceID(instance *v1.KDBInstance) string {
	if instance == nil {
		return ""
	}
	if value := strings.TrimSpace(instance.Labels[naming.LabelScopeInstance]); value != "" {
		return value
	}
	if value := strings.TrimSpace(instance.Labels[InstanceIDLabel]); value != "" {
		return value
	}
	return instance.Name
}

// PlatformInstanceID returns the stable control-plane identity carried by
// monitoring and alert-policy resources during the KDBInstance transition.
func PlatformInstanceID(instance *v1.KDBInstance) string {
	return platformInstanceID(instance)
}

func stringInterfaceMap(values map[string]string) map[string]interface{} {
	result := make(map[string]interface{}, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func boundedReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		return reason[:500]
	}
	return reason
}

func equalStatusProjection(left, right *corev1.ConfigMap) bool {
	return reflect.DeepEqual(left.GetAnnotations(), right.GetAnnotations()) && reflect.DeepEqual(left.GetOwnerReferences(), right.GetOwnerReferences())
}
