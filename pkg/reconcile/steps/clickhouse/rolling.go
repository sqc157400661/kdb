package clickhouse

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
)

const (
	annotationPodRevision        = "clickhouse.kdb.com/pod-revision"
	annotationReloadableRevision = "clickhouse.kdb.com/reloadable-config-revision"
)

func computeGroupRolloutRank(role v1.ClickHouseComputeGroupRole) int {
	switch role {
	case v1.ClickHouseRoleAdhoc:
		return 0
	case v1.ClickHouseRoleServing:
		return 1
	case v1.ClickHouseRoleIngest:
		return 2
	default:
		return 3
	}
}

func canUpdateReplica(readyReplicas, replicasPerShard int32) bool {
	if replicasPerShard <= 1 {
		return readyReplicas >= 1
	}
	return readyReplicas > 1
}

func podRevision(instance *v1.KDBInstance, plan clickHouseHostPlan) string {
	return revisionHash(
		instance.Spec.EngineVersion,
		plan.Group.Name,
		string(plan.Group.Role),
		fmt.Sprintf("%d", replicasPerShard(plan.Group)),
		plan.Group.Instance.MainContainer.Image,
		plan.Group.Instance.SidecarContainer.Image,
		clickHouseBackupRunnerImage(instance, plan.Group.Instance),
	)
}

func reloadableConfigRevision(instance *v1.KDBInstance) string {
	parts := []string{instance.Name, fmt.Sprintf("%d", instance.Spec.ClickHouse.DataShards)}
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		parts = append(parts, group.Name, string(group.Role))
	}
	sort.Strings(parts)
	return revisionHash(parts...)
}

func revisionHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])[:16]
}

func rolloutOrder(plans []clickHouseHostPlan) []clickHouseHostPlan {
	out := append([]clickHouseHostPlan{}, plans...)
	sort.SliceStable(out, func(i, j int) bool {
		ri := computeGroupRolloutRank(out[i].Group.Role)
		rj := computeGroupRolloutRank(out[j].Group.Role)
		if ri != rj {
			return ri < rj
		}
		if out[i].Shard != out[j].Shard {
			return out[i].Shard < out[j].Shard
		}
		return out[i].Replica < out[j].Replica
	})
	return out
}

func canStartVersionUpgrade(instance *v1.KDBInstance, oldVersion string) bool {
	if instance == nil || instance.Spec.EngineVersion == oldVersion {
		return true
	}
	return instance.Annotations != nil && strings.EqualFold(instance.Annotations[annotationBackupReady], "true")
}

func nextRolloutBatch(plans []clickHouseHostPlan, canaryFailed bool) []clickHouseHostPlan {
	if canaryFailed {
		return nil
	}
	ordered := rolloutOrder(plans)
	seenShard := map[int32]struct{}{}
	batch := []clickHouseHostPlan{}
	for _, plan := range ordered {
		if _, ok := seenShard[plan.Shard]; ok {
			continue
		}
		seenShard[plan.Shard] = struct{}{}
		batch = append(batch, plan)
	}
	return batch
}
