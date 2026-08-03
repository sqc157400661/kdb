package clickhouse

import (
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestBuildKeeperStatefulSetsAndService(t *testing.T) {
	instance := standaloneInstanceWithKeeper(3)

	service, err := buildKeeperHeadlessService(instance)
	if err != nil {
		t.Fatalf("buildKeeperHeadlessService() error = %v", err)
	}
	if service == nil || service.Spec.ClusterIP != "None" {
		t.Fatalf("expected keeper headless service, got %#v", service)
	}
	if len(service.Spec.Ports) != 2 {
		t.Fatalf("expected client and raft ports, got %d", len(service.Spec.Ports))
	}

	statefulSets, err := buildKeeperStatefulSets(instance, "kdb-sa")
	if err != nil {
		t.Fatalf("buildKeeperStatefulSets() error = %v", err)
	}
	if len(statefulSets) != 3 {
		t.Fatalf("expected three keeper member StatefulSets, got %d", len(statefulSets))
	}
	for i, sts := range statefulSets {
		if sts.Name != naming.ClickHouseKeeperStatefulSetName(instance.Name, int32(i)) {
			t.Fatalf("unexpected keeper statefulset name: %s", sts.Name)
		}
		if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 1 {
			t.Fatalf("expected one replica per keeper member, got %#v", sts.Spec.Replicas)
		}
		if len(sts.Spec.Template.Spec.Containers) != 1 {
			t.Fatalf("expected one keeper container, got %d", len(sts.Spec.Template.Spec.Containers))
		}
		container := sts.Spec.Template.Spec.Containers[0]
		if container.StartupProbe == nil || container.ReadinessProbe == nil || container.LivenessProbe == nil {
			t.Fatalf("keeper container should define startup, readiness, and liveness probes")
		}
		if len(sts.Spec.VolumeClaimTemplates) != 1 {
			t.Fatalf("expected one keeper data volume claim template")
		}
	}
}

func TestBuildKeeperConfigMapsUseStableRaftMembers(t *testing.T) {
	instance := standaloneInstanceWithKeeper(3)

	configMaps, err := buildKeeperConfigMaps(instance)
	if err != nil {
		t.Fatalf("buildKeeperConfigMaps() error = %v", err)
	}
	if len(configMaps) != 3 {
		t.Fatalf("expected three keeper configmaps, got %d", len(configMaps))
	}
	config := configMaps[1].Data[clickHouseKeeperConfigKey]
	for _, token := range []string{
		"<server_id>2</server_id>",
		"<id>1</id>",
		"<id>2</id>",
		"<id>3</id>",
		naming.ClickHouseKeeperHeadlessServiceName(instance.Name),
	} {
		if !strings.Contains(config, token) {
			t.Fatalf("keeper config should contain %q:\n%s", token, config)
		}
	}
}

func TestPlanNextKeeperMemberChange(t *testing.T) {
	members, err := desiredKeeperMembers(standaloneInstanceWithKeeper(3))
	if err != nil {
		t.Fatalf("desiredKeeperMembers() error = %v", err)
	}
	actions := planNextKeeperMemberChange(members, map[int32]bool{0: true})
	if len(actions) != 1 {
		t.Fatalf("expected exactly one keeper member action, got %#v", actions)
	}
	if actions[0].Member.Index != 1 || actions[0].Action != "CreateOrRepair" {
		t.Fatalf("unexpected next member action: %#v", actions[0])
	}
}

func TestKeeperReplicaReconcileRequiredForShutdownResume(t *testing.T) {
	current := &appsv1.StatefulSet{}
	current.Spec.Replicas = util.Int32(0)
	desired := &appsv1.StatefulSet{}
	desired.Spec.Replicas = util.Int32(1)

	if !keeperReplicaReconcileRequired(current, desired) {
		t.Fatal("keeperReplicaReconcileRequired() = false for shutdown resume")
	}
	current.Spec.Replicas = util.Int32(1)
	if keeperReplicaReconcileRequired(current, desired) {
		t.Fatal("keeperReplicaReconcileRequired() = true for matching replicas")
	}
}

func TestMissingKeeperStatefulSetsIncludesEveryBootstrapPeer(t *testing.T) {
	instance := standaloneInstanceWithKeeper(3)
	desired, err := buildKeeperStatefulSets(instance, "kdb-sa")
	if err != nil {
		t.Fatalf("buildKeeperStatefulSets() error = %v", err)
	}
	existing := map[string]struct{}{desired[0].Name: {}}
	missing := missingKeeperStatefulSets(desired, existing)
	if len(missing) != 2 {
		t.Fatalf("bootstrap must include both peers even while the first member is not ready, got %d", len(missing))
	}
	if missing[0].Name != desired[1].Name || missing[1].Name != desired[2].Name {
		t.Fatalf("unexpected missing Keeper members: %s, %s", missing[0].Name, missing[1].Name)
	}
}

func standaloneInstanceWithKeeper(replicas int32) *v1.KDBInstance {
	instance := standaloneInstance()
	instance.Spec.ClickHouse.Keeper.Mode = v1.ClickHouseKeeperDedicated
	instance.Spec.ClickHouse.Keeper.Replicas = chTestInt32(replicas)
	instance.Spec.ClickHouse.Keeper.Instance = &shared.InstanceSetSpec{
		MainContainer: shared.ContainerSpec{
			Image: "clickhouse-keeper:25.8",
		},
		DataVolumeClaimSpec: shared.PVCSpec{
			StorageClass: "standard",
			Size:         resource.MustParse("10Gi"),
		},
	}
	return instance
}
