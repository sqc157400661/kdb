package monitoring

import (
	"fmt"
	"regexp"
	"strings"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
)

const (
	CloudClusterLabel = "kdb.io/cloud-cluster-id"
	InstanceIDLabel   = "kdb.io/instance-id"
)

// PodTargetRelabelings projects the platform identity carried by a
// KDBInstance into every scraped sample. kdb-admin requires these labels to
// prevent a query for one Cell/instance from observing another one.
func PodTargetRelabelings(instance *v1.KDBInstance) []interface{} {
	if instance == nil {
		return nil
	}
	instanceID := instance.Name
	cloudClusterID := ""
	if instance.Labels != nil {
		if value := instance.Labels[InstanceIDLabel]; value != "" {
			instanceID = value
		}
		if value := instance.Labels[naming.LabelScopeInstance]; value != "" {
			instanceID = value
		}
		cloudClusterID = instance.Labels[CloudClusterLabel]
	}
	out := []interface{}{
		map[string]interface{}{
			"sourceLabels": []interface{}{"__meta_kubernetes_namespace"},
			"targetLabel":  "namespace",
		},
		map[string]interface{}{
			"replacement": instanceID,
			"targetLabel": "instance_id",
		},
		map[string]interface{}{
			"replacement": instance.Name,
			"targetLabel": "kdb_name",
		},
	}
	if cloudClusterID != "" {
		out = append(out,
			map[string]interface{}{
				"replacement": cloudClusterID,
				"targetLabel": "cloud_cluster_id",
			},
			map[string]interface{}{
				"replacement": cloudClusterID,
				"targetLabel": "cell_id",
			},
		)
	}
	for _, item := range platformScopeMetricLabels(instance) {
		if item.target == "instance_id" {
			continue
		}
		out = append(out, map[string]interface{}{
			"replacement": item.value,
			"targetLabel": item.target,
		})
	}
	return out
}

// InstanceResourceRecordingRules converts cluster-wide cAdvisor/kubelet
// samples into instance-scoped series. Those source samples have namespace,
// pod and container/PVC labels but cannot carry PodMonitor target labels such
// as instance_id. Static recording-rule labels preserve the strict kdb-admin
// query contract without relaxing cross-instance query isolation.
func InstanceResourceRecordingRules(instance *v1.KDBInstance) []interface{} {
	if instance == nil {
		return nil
	}
	instanceID := instance.Name
	cloudClusterID := ""
	if instance.Labels != nil {
		if value := instance.Labels[InstanceIDLabel]; value != "" {
			instanceID = value
		}
		if value := instance.Labels[naming.LabelScopeInstance]; value != "" {
			instanceID = value
		}
		cloudClusterID = instance.Labels[CloudClusterLabel]
	}
	labels := map[string]interface{}{
		"instance_id": instanceID,
		"kdb_name":    instance.Name,
	}
	if cloudClusterID != "" {
		labels["cloud_cluster_id"] = cloudClusterID
		labels["cell_id"] = cloudClusterID
	}
	for _, item := range platformScopeMetricLabels(instance) {
		labels[item.target] = item.value
	}
	podPattern, volumePattern := instanceResourcePatterns(instance)
	podMatcher := fmt.Sprintf(`namespace=%q,pod=~%q,container!=""`, instance.Namespace, podPattern)
	volumeMatcher := fmt.Sprintf(`namespace=%q,persistentvolumeclaim=~%q`, instance.Namespace, volumePattern)
	return []interface{}{
		map[string]interface{}{
			"record": "kdb_instance_container_cpu_usage_percent",
			"expr":   fmt.Sprintf(`avg by (namespace,pod,container) (rate(container_cpu_usage_seconds_total{%s}[5m])) * 100`, podMatcher),
			"labels": labels,
		},
		map[string]interface{}{
			"record": "kdb_instance_container_memory_usage_percent",
			"expr": fmt.Sprintf(`avg by (namespace,pod,container) (container_memory_working_set_bytes{%s} / clamp_min(container_spec_memory_limit_bytes{%s}, 1)) * 100`,
				podMatcher, podMatcher),
			"labels": labels,
		},
		map[string]interface{}{
			"record": "kdb_instance_volume_usage_percent",
			"expr": fmt.Sprintf(`avg by (namespace,persistentvolumeclaim) (kubelet_volume_stats_used_bytes{%s} / clamp_min(kubelet_volume_stats_capacity_bytes{%s}, 1)) * 100`,
				volumeMatcher, volumeMatcher),
			"labels": labels,
		},
	}
}

type platformScopeMetricLabel struct {
	target string
	value  string
}

func platformScopeMetricLabels(instance *v1.KDBInstance) []platformScopeMetricLabel {
	if instance == nil {
		return nil
	}
	values := naming.PlatformScopeLabels(instance.Labels)
	keys := []struct {
		source string
		target string
	}{
		{naming.LabelScopeTenant, "tenant_id"},
		{naming.LabelScopeProject, "project_id"},
		{naming.LabelScopeEnvironment, "environment_id"},
		{naming.LabelScopeRegion, "region_id"},
		{naming.LabelScopeInstance, "instance_id"},
	}
	out := make([]platformScopeMetricLabel, 0, len(keys))
	for _, key := range keys {
		if value := values[key.source]; value != "" {
			out = append(out, platformScopeMetricLabel{target: key.target, value: value})
		}
	}
	return out
}

func instanceResourcePatterns(instance *v1.KDBInstance) (string, string) {
	name := regexp.QuoteMeta(instance.Name)
	engine := strings.ToLower(strings.TrimSpace(instance.Spec.Engine))
	if engine == "clickhouse" || instance.Spec.ClickHouse != nil {
		// ClickHouse inserts a delimiter after the KDBInstance name. Keep that
		// delimiter in both selectors so names such as test1 and test123 cannot
		// contribute samples to each other's recording rules.
		return name + `-.*`, `(clickhouse-data|keeper-data)-` + name + `-.*`
	}

	// The legacy MySQL/PostgreSQL StatefulSet name appends the member ordinal
	// directly to the instance name (orders0, orders1, ...). Enumerating the
	// desired ordinals is stricter than a prefix matcher and avoids ambiguity
	// when another instance name extends the same numeric prefix.
	replicas := 1
	if instance.Spec.InstanceSet.Replicas != nil && *instance.Spec.InstanceSet.Replicas > 0 {
		replicas = int(*instance.Spec.InstanceSet.Replicas)
	}
	members := make([]string, 0, replicas)
	for ordinal := 0; ordinal < replicas; ordinal++ {
		members = append(members, fmt.Sprintf("%s%d", name, ordinal))
	}
	memberPattern := "(" + strings.Join(members, "|") + ")"
	return memberPattern + `-.*`, memberPattern + `-.*`
}
