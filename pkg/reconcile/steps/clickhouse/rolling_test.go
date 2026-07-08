package clickhouse

import "testing"

func TestRolloutOrderAndBatch(t *testing.T) {
	instance := replicatedInstance()
	instance.Spec.ClickHouse.ComputeGroups = append(instance.Spec.ClickHouse.ComputeGroups, replicatedGroup("adhoc", "Adhoc"))
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		t.Fatalf("buildClickHouseHostPlans() error = %v", err)
	}
	ordered := rolloutOrder(plans)
	if ordered[0].Group.Role != "Adhoc" {
		t.Fatalf("expected adhoc first, got %s", ordered[0].Group.Role)
	}
	batch := nextRolloutBatch(plans, false)
	if len(batch) != int(instance.Spec.ClickHouse.DataShards) {
		t.Fatalf("expected one rollout candidate per shard, got %d", len(batch))
	}
	if batch := nextRolloutBatch(plans, true); len(batch) != 0 {
		t.Fatalf("expected canary failure to stop rollout, got %#v", batch)
	}
}

func TestVersionUpgradeRequiresBackupReady(t *testing.T) {
	instance := replicatedInstance()
	instance.Spec.EngineVersion = "25.9"
	if canStartVersionUpgrade(instance, "25.8") {
		t.Fatalf("expected version upgrade to require backup readiness")
	}
	instance.Annotations = map[string]string{annotationBackupReady: "true"}
	if !canStartVersionUpgrade(instance, "25.8") {
		t.Fatalf("expected backup-ready version upgrade to pass")
	}
}

func TestRevisionsAreStableAndSeparated(t *testing.T) {
	instance := replicatedInstance()
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		t.Fatalf("buildClickHouseHostPlans() error = %v", err)
	}
	if podRevision(instance, plans[0]) == "" || reloadableConfigRevision(instance) == "" {
		t.Fatalf("expected non-empty revisions")
	}
	before := reloadableConfigRevision(instance)
	instance.Spec.ClickHouse.ComputeGroups[0].Instance.MainContainer.Image = "clickhouse:new"
	if reloadableConfigRevision(instance) != before {
		t.Fatalf("reloadable config revision should not include pod image")
	}
	if podRevision(instance, plans[0]) == podRevision(instance, clickHouseHostPlan{Group: instance.Spec.ClickHouse.ComputeGroups[0]}) {
		t.Fatalf("pod revision should include pod image")
	}
}
