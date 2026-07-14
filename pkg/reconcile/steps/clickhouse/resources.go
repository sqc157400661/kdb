package clickhouse

import (
	"fmt"
	"strings"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/security"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	clickHouseDataVolumeName        = "clickhouse-data"
	clickHouseConfigVolumeName      = "clickhouse-config"
	clickHouseUsersVolumeName       = "clickhouse-users"
	clickHouseSidecarVolumeName     = "clickhouse-sidecar-config"
	clickHouseSecretVolumeName      = "clickhouse-secret"
	clickHouseRuntimeVolumeName     = "clickhouse-runtime"
	clickHouseTmpVolumeName         = "clickhouse-tmp"
	clickHouseLogVolumeName         = "clickhouse-log"
	clickHouseDataMountPath         = "/var/lib/clickhouse"
	clickHouseLogMountPath          = "/var/log/clickhouse-server"
	clickHouseConfigMountPath       = "/etc/clickhouse-server"
	clickHouseSidecarConfigPath     = "/etc/kdb-sidecar"
	clickHouseBackupRunnerContainer = "clickhouse-backup"
	clickHouseBackupRunnerPort      = 7171
)

type standaloneSpec struct {
	group       v1.ClickHouseComputeGroupSpec
	replicas    int32
	statefulSet string
}

func resolveStandaloneSpec(instance *v1.KDBInstance) (standaloneSpec, error) {
	if instance == nil || instance.Spec.ClickHouse == nil {
		return standaloneSpec{}, fmt.Errorf("spec.clickhouse is required when engine is clickhouse")
	}
	spec := instance.Spec.ClickHouse
	if spec.DataShards != 1 {
		return standaloneSpec{}, fmt.Errorf("WP-03 standalone ClickHouse supports exactly one data shard")
	}
	if len(spec.ComputeGroups) != 1 {
		return standaloneSpec{}, fmt.Errorf("WP-03 standalone ClickHouse supports exactly one compute group")
	}
	group := spec.ComputeGroups[0]
	if group.Role != v1.ClickHouseRoleIngest {
		return standaloneSpec{}, fmt.Errorf("WP-03 standalone ClickHouse supports exactly one Ingest compute group")
	}
	replicas := int32(1)
	if group.Instance.Replicas != nil {
		replicas = *group.Instance.Replicas
	}
	if replicas != 1 {
		return standaloneSpec{}, fmt.Errorf("WP-03 standalone ClickHouse supports exactly one replica per shard")
	}
	return standaloneSpec{
		group:       group,
		replicas:    replicas,
		statefulSet: naming.ClickHouseStatefulSetName(instance.Name, group.Name, 0, 0),
	}, nil
}

func buildStandaloneStatefulSet(instance *v1.KDBInstance, serviceAccountName string) (*appsv1.StatefulSet, error) {
	standalone, err := resolveStandaloneSpec(instance)
	if err != nil {
		return nil, err
	}
	return buildClickHouseStatefulSet(instance, clickHouseHostPlan{
		Group:       standalone.group,
		Shard:       0,
		Replica:     0,
		StatefulSet: standalone.statefulSet,
		Labels:      standaloneHostLabels(instance, standalone.group),
	}, serviceAccountName), nil
}

func buildClickHouseStatefulSets(instance *v1.KDBInstance, serviceAccountName string) ([]*appsv1.StatefulSet, error) {
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		return nil, err
	}
	statefulSets := make([]*appsv1.StatefulSet, 0, len(plans))
	for _, plan := range plans {
		statefulSets = append(statefulSets, buildClickHouseStatefulSet(instance, plan, serviceAccountName))
	}
	return statefulSets, nil
}

func buildClickHouseStatefulSet(instance *v1.KDBInstance, plan clickHouseHostPlan, serviceAccountName string) *appsv1.StatefulSet {
	group := plan.Group
	labels := plan.Labels
	replicas := util.Int32(desiredHostReplicas(instance, group, plan.Replica))
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: instance.Namespace,
		Name:      plan.StatefulSet,
	}}
	statefulSet.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("StatefulSet"))
	statefulSet.Annotations = naming.Merge(instance.Annotations, group.Instance.Metadata.GetAnnotationsOrNil())
	statefulSet.Labels = naming.Merge(instance.Labels, group.Instance.Metadata.GetLabelsOrNil(), labels)
	statefulSet.Spec.Replicas = replicas
	statefulSet.Spec.ServiceName = naming.ClickHouseGroupHeadlessServiceName(instance.Name, group.Name)
	statefulSet.Spec.RevisionHistoryLimit = util.Int32(0)
	statefulSet.Spec.UpdateStrategy.Type = appsv1.OnDeleteStatefulSetStrategyType
	statefulSet.Spec.PersistentVolumeClaimRetentionPolicy = &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
		WhenDeleted: appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
		WhenScaled:  appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
	}
	statefulSet.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
	statefulSet.Spec.Template.Annotations = naming.Merge(instance.Annotations, group.Instance.Metadata.GetAnnotationsOrNil())
	if statefulSet.Spec.Template.Annotations == nil {
		statefulSet.Spec.Template.Annotations = map[string]string{}
	}
	statefulSet.Spec.Template.Annotations[annotationPodRevision] = podRevision(instance, plan)
	statefulSet.Spec.Template.Annotations[annotationReloadableRevision] = reloadableConfigRevision(instance)
	statefulSet.Spec.Template.Annotations[annotationEngineVersion] = instance.Spec.EngineVersion
	statefulSet.Spec.Template.Labels = naming.Merge(instance.Labels, group.Instance.Metadata.GetLabelsOrNil(), labels)
	statefulSet.Spec.Template.Labels[naming.LabelClickHouseRoutable] = "false"
	statefulSet.Spec.Template.Spec.ServiceAccountName = serviceAccountName
	statefulSet.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways
	statefulSet.Spec.Template.Spec.ShareProcessNamespace = util.Bool(true)
	statefulSet.Spec.Template.Spec.EnableServiceLinks = util.Bool(false)
	statefulSet.Spec.Template.Spec.Affinity = clickHouseHostAffinity(instance, group, plan)
	statefulSet.Spec.Template.Spec.Tolerations = group.Instance.Tolerations
	statefulSet.Spec.Template.Spec.TopologySpreadConstraints = group.Instance.TopologySpreadConstraints
	if group.Instance.PriorityClassName != nil {
		statefulSet.Spec.Template.Spec.PriorityClassName = *group.Instance.PriorityClassName
	}
	statefulSet.Spec.Template.Spec.SecurityContext = security.PodSecurityContext(instance)
	statefulSet.Spec.Template.Spec.Volumes = standaloneVolumes(instance, group.Name)
	statefulSet.Spec.Template.Spec.Containers = standaloneContainers(instance, group.Instance, plan)
	statefulSet.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{standaloneDataVolumeClaim(instance, group)}
	return statefulSet
}

func clickHouseHostAffinity(instance *v1.KDBInstance, group v1.ClickHouseComputeGroupSpec, plan clickHouseHostPlan) *corev1.Affinity {
	affinity := &corev1.Affinity{}
	if group.Instance.Affinity != nil {
		affinity = group.Instance.Affinity.DeepCopy()
	}
	if replicasPerShard(group) <= 1 {
		return affinity
	}
	if affinity.PodAntiAffinity == nil {
		affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
	}
	term := corev1.PodAffinityTerm{
		LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
			naming.LabelInstance: instance.Name,
			naming.LabelClickHouseEngine: naming.ClickHouseEngine,
			naming.LabelClickHouseComponent: naming.ClickHouseComponentClickHouse,
			naming.LabelClickHouseComputeGroup: group.Name,
			naming.LabelClickHouseDataShard: fmt.Sprintf("%d", plan.Shard),
		}},
		TopologyKey: "kubernetes.io/hostname",
	}
	if group.Instance.Metadata != nil && group.Instance.Metadata.Annotations[annotationRequireCrossNodeReplicas] == "true" {
		affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
			affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution, term)
	} else {
		affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(
			affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
			corev1.WeightedPodAffinityTerm{Weight: 100, PodAffinityTerm: term})
	}
	return affinity
}

func standaloneHostLabels(instance *v1.KDBInstance, group v1.ClickHouseComputeGroupSpec) map[string]string {
	return naming.ClickHouseHostLabels(instance.Name, group.Name, 0, 0)
}

func standaloneDataVolumeClaim(instance *v1.KDBInstance, group v1.ClickHouseComputeGroupSpec) corev1.PersistentVolumeClaim {
	pvcSpec := group.Instance.DataVolumeClaimSpec
	pvc := corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name:        clickHouseDataVolumeName,
		Annotations: naming.Merge(group.Instance.Metadata.GetAnnotationsOrNil(), pvcSpec.Metadata.GetAnnotationsOrNil()),
		Labels: naming.Merge(
			instance.Labels,
			group.Instance.Metadata.GetLabelsOrNil(),
			pvcSpec.Metadata.GetLabelsOrNil(),
			map[string]string{
				naming.LabelInstance:               instance.Name,
				naming.LabelClickHouseEngine:       naming.ClickHouseEngine,
				naming.LabelClickHouseComponent:    naming.ClickHouseComponentClickHouse,
				naming.LabelClickHouseComputeGroup: group.Name,
			}),
	}}
	pvc.Spec = corev1.PersistentVolumeClaimSpec{
		StorageClassName: clickHouseStorageClassName(pvcSpec.StorageClass),
		AccessModes: []corev1.PersistentVolumeAccessMode{
			corev1.ReadWriteOnce,
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: pvcSpec.Size,
			},
		},
	}
	return pvc
}

func clickHouseStorageClassName(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func standaloneVolumes(instance *v1.KDBInstance, group string) []corev1.Volume {
	return []corev1.Volume{
		{
			Name: clickHouseConfigVolumeName,
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: naming.ClickHouseGroupConfigMapName(instance.Name, group)},
				Items: []corev1.KeyToPath{
					{Key: clickHouseRemoteServersKey, Path: "remote_servers.xml"},
					{Key: clickHouseMacrosKey, Path: "macros.xml"},
					{Key: clickHouseKeeperKey, Path: "keeper.xml"},
					{Key: clickHouseNetworkKey, Path: "network.xml"},
					{Key: clickHouseInterserverKey, Path: "interserver.xml"},
					{Key: clickHouseStoragePolicyKey, Path: "storage_policy.xml"},
				},
			}},
		},
		{
			Name: clickHouseUsersVolumeName,
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: naming.ClickHouseGroupConfigMapName(instance.Name, group)},
				Items: []corev1.KeyToPath{
					{Key: clickHouseUsersKey, Path: "users.xml"},
					{Key: clickHouseProfilesKey, Path: "profiles.xml"},
					{Key: clickHouseQuotasKey, Path: "quotas.xml"},
				},
			}},
		},
		{
			Name: clickHouseSidecarVolumeName,
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: naming.ClickHouseGroupConfigMapName(instance.Name, group)},
				Items: []corev1.KeyToPath{{Key: clickHouseSidecarKey, Path: clickHouseSidecarKey}},
			}},
		},
		{
			Name: clickHouseSecretVolumeName,
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: naming.ClickHouseSecretName(instance.Name),
			}},
		},
		{
			Name:         clickHouseRuntimeVolumeName,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
		{
			Name:         clickHouseTmpVolumeName,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
		{
			Name:         clickHouseLogVolumeName,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
	}
}

func standaloneContainers(instance *v1.KDBInstance, instanceSet shared.InstanceSetSpec, plan clickHouseHostPlan) []corev1.Container {
	sharedMounts := []corev1.VolumeMount{
		{Name: clickHouseDataVolumeName, MountPath: clickHouseDataMountPath},
		{Name: clickHouseSecretVolumeName, MountPath: "/etc/clickhouse-secret", ReadOnly: true},
		{Name: clickHouseRuntimeVolumeName, MountPath: "/var/run/kdb"},
		{Name: clickHouseTmpVolumeName, MountPath: "/tmp"},
	}
	databaseMounts := append([]corev1.VolumeMount{}, sharedMounts...)
	databaseMounts = append(databaseMounts,
		corev1.VolumeMount{Name: clickHouseConfigVolumeName, MountPath: clickHouseConfigMountPath + "/config.d", ReadOnly: true},
		corev1.VolumeMount{Name: clickHouseUsersVolumeName, MountPath: clickHouseConfigMountPath + "/users.d", ReadOnly: true},
		corev1.VolumeMount{Name: clickHouseLogVolumeName, MountPath: clickHouseLogMountPath},
	)
	database := corev1.Container{
		Name:            naming.ContainerDatabase,
		Command:         instanceSet.MainContainer.Command,
		Args:            instanceSet.MainContainer.Args,
		Env:             append(clickHouseServerEnv(instance, plan), instanceSet.MainContainer.Env...),
		Image:           instanceSet.MainContainer.Image,
		Resources:       instanceSet.MainContainer.Resources,
		SecurityContext: security.InitClickHouseSecurityContext(),
		Ports: []corev1.ContainerPort{
			{Name: "http", ContainerPort: naming.ClickHouseHTTPPort(), Protocol: corev1.ProtocolTCP},
			{Name: "native", ContainerPort: naming.ClickHouseNativePort(), Protocol: corev1.ProtocolTCP},
		},
		VolumeMounts: databaseMounts,
		StartupProbe: clickHouseHTTPProbe(60, 5),
		ReadinessProbe: clickHouseHTTPProbe(3, 5),
		LivenessProbe: clickHouseHTTPProbe(6, 10),
	}
	sidecarMounts := append([]corev1.VolumeMount{}, sharedMounts...)
	sidecarMounts = append(sidecarMounts,
		corev1.VolumeMount{Name: clickHouseConfigVolumeName, MountPath: clickHouseConfigMountPath + "/config.d", ReadOnly: true},
		corev1.VolumeMount{Name: clickHouseUsersVolumeName, MountPath: clickHouseConfigMountPath + "/users.d", ReadOnly: true},
		corev1.VolumeMount{Name: clickHouseSidecarVolumeName, MountPath: clickHouseSidecarConfigPath, ReadOnly: true},
	)
	sidecarCommand := instanceSet.SidecarContainer.Command
	sidecarArgs := instanceSet.SidecarContainer.Args
	if len(sidecarCommand) == 0 {
		sidecarCommand = []string{"/kdb/bin/manager"}
	}
	if len(sidecarArgs) == 0 {
		sidecarArgs = []string{"clickhouse", "--config", clickHouseSidecarConfigPath + "/" + clickHouseSidecarKey}
	}
	sidecar := corev1.Container{
		Name:            naming.ContainerSidecar,
		Command:         sidecarCommand,
		Args:            sidecarArgs,
		Env:             append(clickHouseClientEnv(instance, plan), instanceSet.SidecarContainer.Env...),
		Image:           instanceSet.SidecarContainer.Image,
		Resources:       instanceSet.SidecarContainer.Resources,
		SecurityContext: security.InitClickHouseSecurityContext(),
		Ports: []corev1.ContainerPort{{
			Name:          naming.PortSidecarMetrics,
			ContainerPort: 8080,
			Protocol:      corev1.ProtocolTCP,
		}},
		VolumeMounts: sidecarMounts,
		StartupProbe: sidecarHTTPProbe(30, 5),
		ReadinessProbe: sidecarHTTPProbe(3, 5),
		LivenessProbe: sidecarHTTPProbe(6, 10),
	}
	containers := []corev1.Container{database, sidecar}
	if !clickHouseBackupRunnerEnabled(instance) {
		return containers
	}
	backupMounts := append([]corev1.VolumeMount{}, sharedMounts...)
	backupMounts = append(backupMounts,
		corev1.VolumeMount{Name: clickHouseConfigVolumeName, MountPath: clickHouseConfigMountPath + "/config.d", ReadOnly: true},
		corev1.VolumeMount{Name: clickHouseUsersVolumeName, MountPath: clickHouseConfigMountPath + "/users.d", ReadOnly: true},
	)
	backupRunner := corev1.Container{
		Name:            clickHouseBackupRunnerContainer,
		Image:           clickHouseBackupRunnerImage(instance, instanceSet),
		Args:            []string{"server"},
		Env: append(clickHouseClientEnv(instance, plan),
			corev1.EnvVar{Name: "API_LISTEN", Value: "127.0.0.1:7171"},
			corev1.EnvVar{Name: "CLICKHOUSE_CONFIG_DIR", Value: clickHouseConfigMountPath},
		),
		Resources:       instanceSet.SidecarContainer.Resources,
		SecurityContext: security.InitClickHouseSecurityContext(),
		Ports: []corev1.ContainerPort{{
			Name:          "backup-api",
			ContainerPort: clickHouseBackupRunnerPort,
			Protocol:      corev1.ProtocolTCP,
		}},
		VolumeMounts: backupMounts,
		StartupProbe: backupRunnerProbe(60, 5),
		ReadinessProbe: backupRunnerProbe(3, 5),
		LivenessProbe: backupRunnerProbe(6, 10),
	}
	if instance.Spec.ClickHouse.Backup != nil && instance.Spec.ClickHouse.Backup.ObjectStorageRef != nil {
		backupRunner.EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: *instance.Spec.ClickHouse.Backup.ObjectStorageRef,
		}}}
	}
	return append(containers, backupRunner)
}

func clickHouseHTTPProbe(failureThreshold, periodSeconds int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
			Path: "/ping",
			Port: intstr.FromString("http"),
		}},
		FailureThreshold: failureThreshold,
		PeriodSeconds: periodSeconds,
		TimeoutSeconds: 3,
	}
}

func sidecarHTTPProbe(failureThreshold, periodSeconds int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
			Path: "/health",
			Port: intstr.FromString(naming.PortSidecarMetrics),
		}},
		FailureThreshold: failureThreshold,
		PeriodSeconds: periodSeconds,
		TimeoutSeconds: 3,
	}
}

func backupRunnerProbe(failureThreshold, periodSeconds int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{
			"sh", "-c", "wget -q -O /dev/null http://127.0.0.1:7171/",
		}}},
		FailureThreshold: failureThreshold,
		PeriodSeconds: periodSeconds,
		TimeoutSeconds: 3,
	}
}

func clickHouseServerEnv(instance *v1.KDBInstance, plan clickHouseHostPlan) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "KDB_INSTANCE_NAME", Value: instance.Name},
		{Name: "KDB_NAMESPACE", Value: instance.Namespace},
		{Name: "KDB_ENGINE", Value: naming.ClickHouseEngine},
		{Name: "KDB_CLICKHOUSE_GROUP", Value: plan.Group.Name},
		{Name: "KDB_CLICKHOUSE_ROLE", Value: string(plan.Group.Role)},
		{Name: "KDB_CLICKHOUSE_SHARD", Value: fmt.Sprintf("%d", plan.Shard)},
		{Name: "KDB_CLICKHOUSE_REPLICA", Value: fmt.Sprintf("%s-r%d", plan.Group.Name, plan.Replica)},
		{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		{Name: "POD_IP", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"}}},
		clickHouseSecretEnv("CLICKHOUSE_ADMIN_PASSWORD", "admin-password", instance),
		clickHouseSecretEnv("CLICKHOUSE_SCHEMA_PASSWORD", "schema-password", instance),
		clickHouseSecretEnv("CLICKHOUSE_SERVING_PASSWORD", "serving-password", instance),
		clickHouseSecretEnv("CLICKHOUSE_ADHOC_PASSWORD", "adhoc-password", instance),
	}
}

func clickHouseClientEnv(instance *v1.KDBInstance, plan clickHouseHostPlan) []corev1.EnvVar {
	return append(clickHouseServerEnv(instance, plan),
		clickHouseSecretEnv("CLICKHOUSE_USERNAME", "admin-username", instance),
		clickHouseSecretEnv("CLICKHOUSE_PASSWORD", "admin-password", instance),
		corev1.EnvVar{Name: "CLICKHOUSE_HOST", Value: "127.0.0.1"},
	)
}

func clickHouseSecretEnv(name, key string, instance *v1.KDBInstance) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: naming.ClickHouseSecretName(instance.Name)},
			Key: key,
		}},
	}
}

func clickHouseBackupRunnerImage(instance *v1.KDBInstance, instanceSet shared.InstanceSetSpec) string {
	if instance != nil && instance.Spec.Config != nil && instance.Spec.Config["clickhouse.backupRunner.image"] != "" {
		return instance.Spec.Config["clickhouse.backupRunner.image"]
	}
	return ""
}

func clickHouseBackupRunnerEnabled(instance *v1.KDBInstance) bool {
	if instance == nil || instance.Spec.ClickHouse == nil {
		return false
	}
	return instance.Spec.ClickHouse.Backup != nil && instance.Spec.ClickHouse.Backup.Enabled != nil && *instance.Spec.ClickHouse.Backup.Enabled
}
