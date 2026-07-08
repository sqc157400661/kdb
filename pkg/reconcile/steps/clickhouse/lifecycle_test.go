package clickhouse

import (
	"testing"
	"time"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdhocLifecycleSuspend(t *testing.T) {
	instance := replicatedInstance()
	adhoc := replicatedGroup("adhoc", v1.ClickHouseRoleAdhoc)
	enabled := true
	autoSuspendAfter := metav1Duration(30 * time.Minute)
	adhoc.Lifecycle = &v1.ClickHouseComputeGroupLifecycleSpec{
		AutoSuspendEnabled: &enabled,
		AutoSuspendAfter:   &autoSuspendAfter,
	}
	instance.Spec.ClickHouse.ComputeGroups = append(instance.Spec.ClickHouse.ComputeGroups, adhoc)
	instance.Annotations = map[string]string{
		annotationGroupLastActivityPrefix + "adhoc": time.Now().Add(-31 * time.Minute).Format(time.RFC3339),
	}

	if !shouldSuspendGroup(instance, adhoc, time.Now()) {
		t.Fatalf("expected idle adhoc group to suspend")
	}
	if desiredHostReplicas(instance, adhoc) != 0 {
		t.Fatalf("expected suspended adhoc host replicas to be zero")
	}
}

func TestReplicaChangeRequiresConfirmation(t *testing.T) {
	oldInstance := replicatedInstance()
	newInstance := replicatedInstance()
	newInstance.Spec.ClickHouse.ComputeGroups[0].Instance.Replicas = chTestInt32(3)

	if err := validateReplicaChangeConfirmation(newInstance, oldInstance); err == nil {
		t.Fatalf("expected replica change confirmation error")
	}
	newInstance.Annotations = map[string]string{
		annotationGroupReplicaChangeConfirmed: "ingest:2->3",
	}
	if err := validateReplicaChangeConfirmation(newInstance, oldInstance); err != nil {
		t.Fatalf("expected confirmed replica change, got %v", err)
	}
}

func metav1Duration(d time.Duration) metav1.Duration {
	return metav1.Duration{Duration: d}
}
