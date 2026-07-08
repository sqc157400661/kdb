package clickhouse

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	annotationGroupSuspendPrefix          = "clickhouse.kdb.com/suspend-"
	annotationGroupLastActivityPrefix     = "clickhouse.kdb.com/last-activity-"
	annotationGroupReplicaChangeConfirmed = "clickhouse.kdb.com/replica-change-confirmed"
	annotationDestructiveDeleteConfirmed  = "clickhouse.kdb.com/destructive-delete-confirmed"
	annotationBackupReady                 = "clickhouse.kdb.com/backup-ready"
	annotationRequireCrossNodeReplicas    = "clickhouse.kdb.com/require-cross-node-replicas"
)

func desiredHostReplicas(instance *v1.KDBInstance, group v1.ClickHouseComputeGroupSpec, replica int32) int32 {
	if instance.Spec.Shutdown != nil && *instance.Spec.Shutdown {
		return 0
	}
	if shouldSuspendGroup(instance, group, time.Now()) {
		if group.Lifecycle != nil && group.Lifecycle.WarmReplicasPerShard != nil && replica < *group.Lifecycle.WarmReplicasPerShard {
			return 1
		}
		return 0
	}
	return 1
}

func shouldSuspendGroup(instance *v1.KDBInstance, group v1.ClickHouseComputeGroupSpec, now time.Time) bool {
	if group.Role != v1.ClickHouseRoleAdhoc {
		return false
	}
	if groupSuspendedExplicitly(instance, group.Name) {
		return true
	}
	if group.Lifecycle == nil || group.Lifecycle.AutoSuspendEnabled == nil || !*group.Lifecycle.AutoSuspendEnabled {
		return false
	}
	if hasBlockingOperation(instance) {
		return false
	}
	idleAfter := 30 * time.Minute
	if group.Lifecycle.AutoSuspendAfter != nil && group.Lifecycle.AutoSuspendAfter.Duration > 0 {
		idleAfter = group.Lifecycle.AutoSuspendAfter.Duration
	}
	last := groupLastActivity(instance, group.Name)
	return !last.IsZero() && now.Sub(last) >= idleAfter
}

func groupSuspendedExplicitly(instance *v1.KDBInstance, group string) bool {
	if instance == nil || instance.Annotations == nil {
		return false
	}
	return strings.EqualFold(instance.Annotations[annotationGroupSuspendPrefix+group], "true")
}

func groupLastActivity(instance *v1.KDBInstance, group string) time.Time {
	if instance == nil || instance.Annotations == nil {
		return time.Time{}
	}
	value := strings.TrimSpace(instance.Annotations[annotationGroupLastActivityPrefix+group])
	if value == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, value)
	return t
}

func hasBlockingOperation(instance *v1.KDBInstance) bool {
	if instance == nil || instance.Annotations == nil {
		return false
	}
	for key, value := range instance.Annotations {
		if strings.HasPrefix(key, "clickhouse.kdb.com/operation-") && strings.EqualFold(value, "running") {
			return true
		}
	}
	return false
}

func replicaChangeConfirmed(instance *v1.KDBInstance, group string, oldReplicas, newReplicas int32) bool {
	if oldReplicas == newReplicas {
		return true
	}
	if instance == nil || instance.Annotations == nil {
		return false
	}
	expected := fmt.Sprintf("%s:%d->%d", group, oldReplicas, newReplicas)
	return instance.Annotations[annotationGroupReplicaChangeConfirmed] == expected
}

func validateReplicaChangeConfirmation(instance, oldInstance *v1.KDBInstance) error {
	if instance == nil || oldInstance == nil || instance.Spec.ClickHouse == nil || oldInstance.Spec.ClickHouse == nil {
		return nil
	}
	oldGroups := map[string]int32{}
	for _, group := range oldInstance.Spec.ClickHouse.ComputeGroups {
		oldGroups[group.Name] = replicasPerShard(group)
	}
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		oldReplicas, ok := oldGroups[group.Name]
		if !ok {
			continue
		}
		newReplicas := replicasPerShard(group)
		if !replicaChangeConfirmed(instance, group.Name, oldReplicas, newReplicas) {
			return fmt.Errorf("clickhouse replicasPerShard change for %s requires confirmation annotation %s=%s", group.Name, annotationGroupReplicaChangeConfirmed, group.Name+":"+strconv.FormatInt(int64(oldReplicas), 10)+"->"+strconv.FormatInt(int64(newReplicas), 10))
		}
	}
	return nil
}

func setProgressingCondition(instance *v1.KDBInstance, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               "Progressing",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: instance.Generation,
	})
}
