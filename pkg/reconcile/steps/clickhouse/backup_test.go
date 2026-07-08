package clickhouse

import "testing"

func TestSelectBackupTargetsOneHealthyIngestPerShard(t *testing.T) {
	instance := replicatedInstance()
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		t.Fatalf("buildClickHouseHostPlans() error = %v", err)
	}
	candidates := make([]backupReplicaCandidate, 0, len(plans))
	for _, plan := range plans {
		candidates = append(candidates, backupReplicaCandidate{Plan: plan, Healthy: true})
	}

	targets, err := selectBackupTargets(candidates, instance.Spec.ClickHouse.DataShards, 10)
	if err != nil {
		t.Fatalf("selectBackupTargets() error = %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected one target per shard, got %d", len(targets))
	}
	for _, target := range targets {
		if target.Plan.Group.Name != "ingest" {
			t.Fatalf("backup target should be ingest, got %#v", target.Plan)
		}
	}
}

func TestSelectBackupTargetsRejectsUnhealthyReadonlyDiskFailedAndRestoring(t *testing.T) {
	instance := replicatedInstance()
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		t.Fatalf("buildClickHouseHostPlans() error = %v", err)
	}
	candidates := []backupReplicaCandidate{
		{Plan: plans[0], Healthy: false},
		{Plan: plans[1], Healthy: true, Readonly: true},
		{Plan: plans[2], Healthy: true, DiskFailed: true},
		{Plan: plans[3], Healthy: true, Restoring: true},
	}
	if _, err := selectBackupTargets(candidates, instance.Spec.ClickHouse.DataShards, 10); err == nil {
		t.Fatalf("expected no eligible backup target")
	}
}

func TestAggregateShardBackupResultsRetainsPartialArtifacts(t *testing.T) {
	phase, artifacts := aggregateShardBackupResults([]shardBackupResult{
		{Shard: 0, Phase: "Succeeded", Artifacts: []string{"s0.tar"}},
		{Shard: 1, Phase: "Failed", Artifacts: []string{"s1.partial"}},
	})
	if phase != "Failed" {
		t.Fatalf("expected aggregate failed phase, got %s", phase)
	}
	if len(artifacts) != 2 {
		t.Fatalf("expected retained artifacts, got %#v", artifacts)
	}
}
