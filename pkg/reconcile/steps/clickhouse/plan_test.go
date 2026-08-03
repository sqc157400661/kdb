package clickhouse

import (
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestBuildClickHouseHostPlansMatrix(t *testing.T) {
	instance := replicatedInstance()

	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		t.Fatalf("buildClickHouseHostPlans() error = %v", err)
	}
	if len(plans) != 8 {
		t.Fatalf("expected 8 host plans for 2 shards * 2 groups * 2 replicas, got %d", len(plans))
	}
	first := plans[0]
	if first.Group.Name != "ingest" || first.Shard != 0 || first.Replica != 0 {
		t.Fatalf("unexpected first plan: %#v", first)
	}
	last := plans[len(plans)-1]
	if last.Group.Name != "serving" || last.Shard != 1 || last.Replica != 1 {
		t.Fatalf("unexpected last plan: %#v", last)
	}
}

func TestRenderClickHouseRemoteServersIncludesGroupsAndAllReplicas(t *testing.T) {
	instance := replicatedInstance()
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		t.Fatalf("buildClickHouseHostPlans() error = %v", err)
	}
	config := renderClickHouseRemoteServers(instance, plans, "ingest")
	for _, token := range []string{
		"kdb_ingest_group",
		"kdb_serving_group",
		"kdb_all_replicas",
		"analytics-ch-ingest-s0-r0-0.analytics-ch-ingest-headless.kdb.svc",
		"analytics-ch-serving-s1-r1-0.analytics-ch-serving-headless.kdb.svc",
		"<user>kdb_admin</user>",
		"<password from_env=\"CLICKHOUSE_ADMIN_PASSWORD\"/>",
	} {
		if !strings.Contains(config, token) {
			t.Fatalf("remote_servers missing %q:\n%s", token, config)
		}
	}
	if strings.Contains(config, "<password>kdb") {
		t.Fatalf("remote_servers must never render a plaintext password:\n%s", config)
	}
	if replicas, users := strings.Count(config, "<replica>"), strings.Count(config, "<user>kdb_admin</user>"); replicas != users {
		t.Fatalf("every remote replica must carry explicit credentials: replicas=%d users=%d\n%s", replicas, users, config)
	}
}

func TestBuildClickHouseServicesPerGroup(t *testing.T) {
	instance := replicatedInstance()

	services, err := buildClickHouseServices(instance)
	if err != nil {
		t.Fatalf("buildClickHouseServices() error = %v", err)
	}
	if len(services) != 4 {
		t.Fatalf("expected headless and client service per group, got %d", len(services))
	}
	if services[0].Spec.Selector[naming.LabelClickHouseComputeGroup] != "ingest" {
		t.Fatalf("unexpected first service selector: %#v", services[0].Spec.Selector)
	}
	if services[2].Spec.Selector[naming.LabelClickHouseComputeGroup] != "serving" {
		t.Fatalf("unexpected serving service selector: %#v", services[2].Spec.Selector)
	}
}

func TestReplicaRoutable(t *testing.T) {
	if !replicaRoutable(sidecarStatus{Healthy: true, ReplicationDelaySeconds: 10}, 10) {
		t.Fatalf("healthy replica at threshold should be routable")
	}
	if replicaRoutable(sidecarStatus{Healthy: true, Readonly: true}, 10) {
		t.Fatalf("readonly replica should not be routable")
	}
	if replicaRoutable(sidecarStatus{Healthy: true, ReplicationDelaySeconds: 11}, 10) {
		t.Fatalf("lagged replica should not be routable")
	}
}

func replicatedInstance() *v1.KDBInstance {
	instance := standaloneInstanceWithKeeper(3)
	instance.Spec.ClickHouse.DataShards = 2
	instance.Spec.ClickHouse.ComputeGroups = []v1.ClickHouseComputeGroupSpec{
		replicatedGroup("ingest", v1.ClickHouseRoleIngest),
		replicatedGroup("serving", v1.ClickHouseRoleServing),
	}
	return instance
}

func replicatedGroup(name string, role v1.ClickHouseComputeGroupRole) v1.ClickHouseComputeGroupSpec {
	return v1.ClickHouseComputeGroupSpec{
		Name: name,
		Role: role,
		Instance: shared.InstanceSetSpec{
			Replicas: chTestInt32(2),
			MainContainer: shared.ContainerSpec{
				Image: "clickhouse:25.8",
			},
			SidecarContainer: shared.ContainerSpec{
				Image: "kdb-sidecar:latest",
			},
			DataVolumeClaimSpec: shared.PVCSpec{
				StorageClass: "standard",
				Size:         resource.MustParse("20Gi"),
			},
		},
	}
}
