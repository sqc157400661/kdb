package clickhouse

import (
	"fmt"
	"strings"

	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	internalmonitoring "github.com/sqc157400661/kdb/internal/monitoring"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// SetMonitor installs the ClickHouse sidecar scrape contract and the small
// set of alerts required for availability scoring. Prometheus Operator CRDs
// are an optional platform capability: an absent CRD must not block the KDB
// instance itself from reconciling.
func (s *InstanceStepManager) SetMonitor() kube.BindFunc {
	return s.StepBinder("ClickHouseSetMonitor", func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
		if rc == nil || rc.GetInstance() == nil || rc.GetInstance().Spec.ClickHouse == nil {
			return flow.Pass()
		}
		instance := rc.GetInstance()
		for _, obj := range clickHouseMonitoringObjects(instance) {
			if err := rc.SetOwnerReference(obj); err != nil {
				return flow.Error(err, "set ClickHouse monitor owner ref err")
			}
			if err := rc.Apply(obj); err != nil {
				if strings.Contains(err.Error(), "no matches for kind") || strings.Contains(err.Error(), "the server could not find the requested resource") {
					return flow.Pass()
				}
				return flow.Error(err, "apply ClickHouse monitor resource err")
			}
		}
		return flow.Pass()
	})
}

func clickHouseMonitoringObjects(instance *v1.KDBInstance) []*unstructured.Unstructured {
	labels := map[string]interface{}{
		"app.kubernetes.io/name":       instance.Name,
		"app.kubernetes.io/managed-by": "kdb-operator",
		"kdb.io/monitoring":            "enabled",
		naming.LabelInstance:           instance.Name,
		naming.LabelClickHouseEngine:   naming.ClickHouseEngine,
	}
	selector := map[string]interface{}{"matchLabels": map[string]interface{}{
		naming.LabelInstance:            instance.Name,
		naming.LabelClickHouseEngine:    naming.ClickHouseEngine,
		naming.LabelClickHouseComponent: naming.ClickHouseComponentClickHouse,
	}}
	tlsConfig := map[string]interface{}{
		"ca": map[string]interface{}{"secret": map[string]interface{}{
			"name": naming.ClickHouseSecretName(instance.Name), "key": naming.ClickHouseTLSCAKey,
		}},
		"cert": map[string]interface{}{"secret": map[string]interface{}{
			"name": naming.ClickHouseSecretName(instance.Name), "key": naming.ClickHouseTLSClientCertKey,
		}},
		"keySecret":          map[string]interface{}{"name": naming.ClickHouseSecretName(instance.Name), "key": naming.ClickHouseTLSClientPrivateKey},
		"insecureSkipVerify": true,
	}
	podMonitor := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PodMonitor",
		"metadata": map[string]interface{}{
			"name":      instance.Name + "-clickhouse",
			"namespace": instance.Namespace,
			"labels":    labels,
		},
		"spec": map[string]interface{}{
			"namespaceSelector": map[string]interface{}{"matchNames": []interface{}{instance.Namespace}},
			"selector":          selector,
			"podMetricsEndpoints": []interface{}{map[string]interface{}{
				"port":        naming.PortSidecarMetrics,
				"path":        "/metrics",
				"scheme":      "https",
				"interval":    "15s",
				"relabelings": internalmonitoring.PodTargetRelabelings(instance),
				"tlsConfig":   tlsConfig,
			}},
		},
	}}
	podMonitor.SetGroupVersionKind(schema.FromAPIVersionAndKind("monitoring.coreos.com/v1", "PodMonitor"))

	podMatcher := fmt.Sprintf(`namespace="%s",pod=~"%s.*"`, instance.Namespace, instance.Name)
	rules := internalmonitoring.InstanceResourceRecordingRules(instance)
	rules = append(rules,
		clickHouseAlert("KDBClickHouseDown", fmt.Sprintf(`kdb_clickhouse_up{%s} == 0`, podMatcher), "2m", "critical", "ClickHouse endpoint is unreachable"),
		clickHouseAlert("KDBClickHouseSQLProbeDown", fmt.Sprintf(`kdb_clickhouse_sql_up{%s} == 0`, podMatcher), "2m", "critical", "ClickHouse read-only SQL probe is failing"),
		clickHouseAlert("KDBClickHouseSidecarDown", fmt.Sprintf(`kdb_sidecar_up{%s,component="clickhouse-sidecar"} == 0`, podMatcher), "2m", "critical", "ClickHouse sidecar metrics endpoint is down"),
		clickHouseAlert("KDBClickHouseDiskPressure", fmt.Sprintf(`max(kubelet_volume_stats_used_bytes{namespace="%s",persistentvolumeclaim=~".*%s.*"} / kubelet_volume_stats_capacity_bytes{namespace="%s",persistentvolumeclaim=~".*%s.*"}) > 0.85`, instance.Namespace, instance.Name, instance.Namespace, instance.Name), "10m", "warning", "ClickHouse volume usage exceeds 85 percent"),
	)
	rule := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata": map[string]interface{}{
			"name":      instance.Name + "-clickhouse",
			"namespace": instance.Namespace,
			"labels":    labels,
		},
		"spec": map[string]interface{}{
			"groups": []interface{}{map[string]interface{}{
				"name":  "kdb.clickhouse." + instance.Name,
				"rules": rules,
			}},
		},
	}}
	rule.SetGroupVersionKind(schema.FromAPIVersionAndKind("monitoring.coreos.com/v1", "PrometheusRule"))
	return []*unstructured.Unstructured{podMonitor, rule}
}

func clickHouseAlert(name, expr, duration, severity, summary string) map[string]interface{} {
	return map[string]interface{}{
		"alert": name,
		"expr":  expr,
		"for":   duration,
		"labels": map[string]interface{}{
			"severity": severity,
			"engine":   naming.ClickHouseEngine,
		},
		"annotations": map[string]interface{}{"summary": summary},
	}
}
