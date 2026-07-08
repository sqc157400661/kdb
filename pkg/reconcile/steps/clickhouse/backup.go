package clickhouse

import "fmt"

type backupReplicaCandidate struct {
	Plan                    clickHouseHostPlan
	Healthy                 bool
	Readonly                bool
	DiskFailed              bool
	Restoring               bool
	ReplicationDelaySeconds int64
}

type shardBackupTarget struct {
	Shard int32
	Plan  clickHouseHostPlan
}

type shardBackupResult struct {
	Shard     int32
	Phase     string
	Artifacts []string
	Message   string
}

func selectBackupTargets(candidates []backupReplicaCandidate, dataShards int32, maxDelaySeconds int64) ([]shardBackupTarget, error) {
	selected := map[int32]shardBackupTarget{}
	for _, candidate := range candidates {
		if candidate.Plan.Group.Role != "Ingest" {
			continue
		}
		if !backupCandidateEligible(candidate, maxDelaySeconds) {
			continue
		}
		if _, exists := selected[candidate.Plan.Shard]; exists {
			continue
		}
		selected[candidate.Plan.Shard] = shardBackupTarget{Shard: candidate.Plan.Shard, Plan: candidate.Plan}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no healthy ingest replica available for backup")
	}
	targets := make([]shardBackupTarget, 0, dataShards)
	for shard := int32(0); shard < dataShards; shard++ {
		target, ok := selected[shard]
		if !ok {
			return nil, fmt.Errorf("no healthy ingest replica available for shard %d", shard)
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func backupCandidateEligible(candidate backupReplicaCandidate, maxDelaySeconds int64) bool {
	if !candidate.Healthy || candidate.Readonly || candidate.DiskFailed || candidate.Restoring {
		return false
	}
	return candidate.ReplicationDelaySeconds <= maxDelaySeconds
}

func aggregateShardBackupResults(results []shardBackupResult) (string, []string) {
	phase := "Succeeded"
	artifacts := []string{}
	for _, result := range results {
		artifacts = append(artifacts, result.Artifacts...)
		if result.Phase == "Failed" {
			phase = "Failed"
		}
	}
	return phase, artifacts
}
