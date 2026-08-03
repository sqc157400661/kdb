package monitoring

import (
	"regexp"
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodTargetRelabelingsCarryStrictPlatformScope(t *testing.T) {
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{
		Name: "mysql-orders",
		Labels: map[string]string{
			CloudClusterLabel: "cell-a",
			InstanceIDLabel:   "instance-a",
		},
	}}
	relabelings := PodTargetRelabelings(instance)
	got := map[string]string{}
	for _, raw := range relabelings {
		item, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("relabeling type = %T", raw)
		}
		target, _ := item["targetLabel"].(string)
		replacement, _ := item["replacement"].(string)
		got[target] = replacement
	}
	for key, want := range map[string]string{
		"cloud_cluster_id": "cell-a",
		"cell_id":          "cell-a",
		"instance_id":      "instance-a",
		"kdb_name":         "mysql-orders",
	} {
		if got[key] != want {
			t.Fatalf("%s replacement = %q, want %q", key, got[key], want)
		}
	}
}

func TestInstanceResourceRecordingRulesAttachStrictPlatformScope(t *testing.T) {
	replicas := int32(2)
	instance := &v1.KDBInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders",
			Namespace: "kdb",
			Labels: map[string]string{
				CloudClusterLabel: "cell-a",
				InstanceIDLabel:   "instance-a",
			},
		},
		Spec: v1.KDBInstanceSpec{Engine: "mysql", InstanceSet: shared.InstanceSetSpec{Replicas: &replicas}},
	}
	rules := InstanceResourceRecordingRules(instance)
	if len(rules) != 3 {
		t.Fatalf("resource recording rules = %d, want cpu, memory, and volume", len(rules))
	}
	wantRecords := map[string]bool{
		"kdb_instance_container_cpu_usage_percent":    false,
		"kdb_instance_container_memory_usage_percent": false,
		"kdb_instance_volume_usage_percent":           false,
	}
	for _, raw := range rules {
		rule, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("recording rule type = %T", raw)
		}
		record, _ := rule["record"].(string)
		if _, ok := wantRecords[record]; !ok {
			t.Fatalf("unexpected record name %q", record)
		}
		wantRecords[record] = true
		labels, _ := rule["labels"].(map[string]interface{})
		for key, want := range map[string]string{
			"cloud_cluster_id": "cell-a",
			"cell_id":          "cell-a",
			"instance_id":      "instance-a",
			"kdb_name":         "orders",
		} {
			if labels[key] != want {
				t.Fatalf("record %s label %s = %v, want %q", record, key, labels[key], want)
			}
		}
		expr, _ := rule["expr"].(string)
		instanceSelector := `pod=~"(orders0|orders1)-.*"`
		if record == "kdb_instance_volume_usage_percent" {
			instanceSelector = `persistentvolumeclaim=~"(orders0|orders1)-.*"`
		}
		if !strings.Contains(expr, `namespace="kdb"`) || !strings.Contains(expr, instanceSelector) {
			t.Fatalf("record %s is not scoped to the instance pods: %q", record, expr)
		}
	}
	for record, found := range wantRecords {
		if !found {
			t.Fatalf("record %s is missing", record)
		}
	}
}

func TestInstanceResourcePatternsDoNotOverlapNumericNamePrefixes(t *testing.T) {
	clickHouse := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "test1"}, Spec: v1.KDBInstanceSpec{Engine: "clickhouse"}}
	podPattern, volumePattern := instanceResourcePatterns(clickHouse)
	if !regexp.MustCompile("^("+podPattern+")$").MatchString("test1-ch-ingest-s0-r0-0") ||
		regexp.MustCompile("^("+podPattern+")$").MatchString("test123-ch-ingest-s0-r0-0") {
		t.Fatalf("ClickHouse pod pattern %q overlaps a longer instance name", podPattern)
	}
	if !regexp.MustCompile("^("+volumePattern+")$").MatchString("clickhouse-data-test1-ch-ingest-s0-r0-0") ||
		regexp.MustCompile("^("+volumePattern+")$").MatchString("clickhouse-data-test123-ch-ingest-s0-r0-0") {
		t.Fatalf("ClickHouse volume pattern %q overlaps a longer instance name", volumePattern)
	}

	replicas := int32(2)
	mysql := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "mysql1"}, Spec: v1.KDBInstanceSpec{
		Engine: "mysql", InstanceSet: shared.InstanceSetSpec{Replicas: &replicas},
	}}
	podPattern, _ = instanceResourcePatterns(mysql)
	matcher := regexp.MustCompile("^(" + podPattern + ")$")
	if !matcher.MatchString("mysql10-0") || !matcher.MatchString("mysql11-0") || matcher.MatchString("mysql123-0") {
		t.Fatalf("MySQL pod pattern %q does not enumerate exact member ordinals", podPattern)
	}
}
