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
	if !containerMountsPath(sts.Spec.Template.Spec.Containers[0], naming.DataMountPath) ||
		!containerMountsPath(sts.Spec.Template.Spec.Containers[1], naming.DataMountPath) {
		t.Fatalf("database and sidecar must share canonical data root %s", naming.DataMountPath)
	}
	if !podHasVolume(sts.Spec.Template.Spec.Volumes, clickHouseTmpVolumeName) {
		t.Fatalf("pod should define writable %s emptyDir volume", clickHouseTmpVolumeName)
	}
	for _, container := range sts.Spec.Template.Spec.Containers {
		if !containerMountsPath(container, "/tmp") {
			t.Fatalf("%s container should mount writable /tmp", container.Name)
		}
	}
	if len(sts.Spec.Template.Spec.InitContainers) != 1 || sts.Spec.Template.Spec.InitContainers[0].Name != "database-log-init" ||
		!containerMountsPath(sts.Spec.Template.Spec.InitContainers[0], naming.DataMountPath) ||
		!strings.Contains(strings.Join(sts.Spec.Template.Spec.InitContainers[0].Command, " "), naming.DatabaseLogRoot) {
		t.Fatalf("canonical log directory init is missing: %#v", sts.Spec.Template.Spec.InitContainers)
	}
	if containerHasEnv(sts.Spec.Template.Spec.Containers[0], "CLICKHOUSE_PASSWORD") {
		t.Fatalf("database container must not set CLICKHOUSE_PASSWORD because the ClickHouse entrypoint writes users.d/default-user.xml when it is present")
	}
	if !containerHasEnv(sts.Spec.Template.Spec.Containers[0], "CLICKHOUSE_ADMIN_PASSWORD") {
		t.Fatalf("database container should keep CLICKHOUSE_ADMIN_PASSWORD for users.xml from_env")
	}
	if !containerHasEnv(sts.Spec.Template.Spec.Containers[1], "CLICKHOUSE_PASSWORD") {
		t.Fatalf("sidecar container should keep CLICKHOUSE_PASSWORD for local client access")
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
		clickHouseNetworkKey,
		clickHouseInterserverKey,
		clickHouseStoragePolicyKey,
		clickHouseLoggingKey,
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
	if !strings.Contains(cm.Data[clickHouseLoggingKey], naming.DatabaseLogRoot+"/clickhouse-server.log") ||
		!strings.Contains(cm.Data[clickHouseLoggingKey], naming.DatabaseLogRoot+"/clickhouse-server-error.log") {
		t.Fatalf("clickhouse file logs are not canonical: %s", cm.Data[clickHouseLoggingKey])
	}
	if !strings.Contains(cm.Data[clickHouseNetworkKey], "<listen_host>0.0.0.0</listen_host>") {
		t.Fatalf("network config should allow kubelet probes and Services to reach ClickHouse on the Pod IP")
	}
	if !strings.Contains(cm.Data[clickHouseUsersKey], "<kdb_admin>") || !strings.Contains(cm.Data[clickHouseUsersKey], "GRANT ALL ON *.* WITH GRANT OPTION") {
		t.Fatalf("users config should define the dedicated access-management account with grant option")
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
	if string(secret.Data["admin-username"]) != "kdb_admin" {
		t.Fatalf("admin username = %q, want kdb_admin", string(secret.Data["admin-username"]))
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
				Backup:     &v1.ClickHouseBackupSpec{Enabled: chTestBool(true)},
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

func podHasVolume(volumes []corev1.Volume, name string) bool {
	for _, volume := range volumes {
		if volume.Name == name {
			return true
		}
	}
	return false
}

func containerHasEnv(container corev1.Container, name string) bool {
	for _, env := range container.Env {
		if env.Name == name {
			return true
		}
	}
	return false
}
