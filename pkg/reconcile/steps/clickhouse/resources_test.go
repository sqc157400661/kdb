package clickhouse

import (
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildStandaloneStatefulSet(t *testing.T) {
	instance := standaloneInstance()

	sts, err := buildStandaloneStatefulSet(instance, "kdb-sa")
	if err != nil {
		t.Fatalf("buildStandaloneStatefulSet() error = %v", err)
	}
	if sts.Name != "analytics-ch-ingest-s0-r0" {
		t.Fatalf("unexpected statefulset name: %s", sts.Name)
	}
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 1 {
		t.Fatalf("expected one StatefulSet replica, got %#v", sts.Spec.Replicas)
	}
	if sts.Spec.ServiceName != naming.ClickHouseGroupHeadlessServiceName(instance.Name, "ingest") {
		t.Fatalf("unexpected service name: %s", sts.Spec.ServiceName)
	}
	if len(sts.Spec.VolumeClaimTemplates) != 1 {
		t.Fatalf("expected one data volume claim template, got %d", len(sts.Spec.VolumeClaimTemplates))
	}
	if sts.Spec.VolumeClaimTemplates[0].Name != clickHouseDataVolumeName {
		t.Fatalf("unexpected data volume template name: %s", sts.Spec.VolumeClaimTemplates[0].Name)
	}
	if len(sts.Spec.Template.Spec.Containers) != 3 {
		t.Fatalf("expected clickhouse, sidecar, and backup runner containers, got %d", len(sts.Spec.Template.Spec.Containers))
	}
	if sts.Spec.Template.Spec.Containers[0].Name != naming.ContainerDatabase {
		t.Fatalf("first container should be database, got %s", sts.Spec.Template.Spec.Containers[0].Name)
	}
	if !containerMountsPath(sts.Spec.Template.Spec.Containers[0], clickHouseDataMountPath) {
		t.Fatalf("database container should mount %s", clickHouseDataMountPath)
	}
}

func TestBuildStandaloneStatefulSetHonorsShutdown(t *testing.T) {
	instance := standaloneInstance()
	enabled := true
	instance.Spec.Shutdown = &enabled

	sts, err := buildStandaloneStatefulSet(instance, "kdb-sa")
	if err != nil {
		t.Fatalf("buildStandaloneStatefulSet() error = %v", err)
	}
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 0 {
		t.Fatalf("expected shutdown standalone StatefulSet replicas to be zero, got %#v", sts.Spec.Replicas)
	}
}

func TestBuildStandaloneConfigMapAndSecret(t *testing.T) {
	instance := standaloneInstance()

	cm, err := buildStandaloneConfigMap(instance)
	if err != nil {
		t.Fatalf("buildStandaloneConfigMap() error = %v", err)
	}
	for _, key := range []string{
		clickHouseRemoteServersKey,
		clickHouseMacrosKey,
		clickHouseKeeperKey,
		clickHouseInterserverKey,
		clickHouseStoragePolicyKey,
		clickHouseUsersKey,
		clickHouseProfilesKey,
		clickHouseQuotasKey,
		clickHouseSidecarKey,
	} {
		if cm.Data[key] == "" {
			t.Fatalf("expected config key %s", key)
		}
	}
	if !strings.Contains(cm.Data[clickHouseRemoteServersKey], "kdb_ingest_group") {
		t.Fatalf("remote_servers should include kdb_ingest_group")
	}

	secret, err := buildStandaloneSecret(instance)
	if err != nil {
		t.Fatalf("buildStandaloneSecret() error = %v", err)
	}
	if secret.Name != naming.ClickHouseSecretName(instance.Name) {
		t.Fatalf("unexpected secret name: %s", secret.Name)
	}
	if _, ok := secret.Data["schema-username"]; !ok {
		t.Fatalf("secret should include schema account material")
	}
}

func TestBuildStandaloneServices(t *testing.T) {
	instance := standaloneInstance()

	headless, clientService, err := buildStandaloneServices(instance)
	if err != nil {
		t.Fatalf("buildStandaloneServices() error = %v", err)
	}
	if headless.Spec.ClusterIP != corev1.ClusterIPNone {
		t.Fatalf("expected headless service, got ClusterIP=%q", headless.Spec.ClusterIP)
	}
	if clientService.Spec.ClusterIP == corev1.ClusterIPNone {
		t.Fatalf("client service must not be headless")
	}
	if headless.Spec.Selector[naming.LabelClickHouseComputeGroup] != "ingest" {
		t.Fatalf("service selector should include compute group")
	}
	if len(headless.Spec.Ports) != 2 {
		t.Fatalf("expected http and native ports, got %d", len(headless.Spec.Ports))
	}
}

func TestResolveStandaloneSpecRejectsNonStandaloneTopology(t *testing.T) {
	cases := []struct {
		name   string
		modify func(*v1.KDBInstance)
	}{
		{
			name: "two shards",
			modify: func(instance *v1.KDBInstance) {
				instance.Spec.ClickHouse.DataShards = 2
			},
		},
		{
			name: "serving group",
			modify: func(instance *v1.KDBInstance) {
				instance.Spec.ClickHouse.ComputeGroups[0].Role = v1.ClickHouseRoleServing
			},
		},
		{
			name: "two replicas",
			modify: func(instance *v1.KDBInstance) {
				instance.Spec.ClickHouse.ComputeGroups[0].Instance.Replicas = chTestInt32(2)
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			instance := standaloneInstance()
			tt.modify(instance)
			if _, err := resolveStandaloneSpec(instance); err == nil {
				t.Fatalf("expected non-standalone topology to be rejected")
			}
		})
	}
}

func standaloneInstance() *v1.KDBInstance {
	return &v1.KDBInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "analytics", Namespace: "kdb"},
		Spec: v1.KDBInstanceSpec{
			Engine:        naming.ClickHouseEngine,
			EngineVersion: "25.8",
			Config: map[string]string{
				"clickhouse.backupRunner.image": "clickhouse-backup:latest",
			},
			ClickHouse: &v1.ClickHouseSpec{
				DataShards: 1,
				Keeper: v1.ClickHouseKeeperSpec{
					Mode:     v1.ClickHouseKeeperDedicated,
					Replicas: chTestInt32(1),
				},
				ComputeGroups: []v1.ClickHouseComputeGroupSpec{{
					Name: "ingest",
					Role: v1.ClickHouseRoleIngest,
					Instance: shared.InstanceSetSpec{
						Replicas: chTestInt32(1),
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
				}},
			},
		},
	}
}

func chTestInt32(v int32) *int32 {
	return &v
}

func containerMountsPath(container corev1.Container, path string) bool {
	for _, mount := range container.VolumeMounts {
		if mount.MountPath == path {
			return true
		}
	}
	return false
}
