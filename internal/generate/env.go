package generate

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/topology"
)

// RequestEnvironment returns the environment variables required to invoke kdb utilities.
func RequestEnvironment(instance *v1.KDBInstance, statefulSetName string) []corev1.EnvVar {
	ordinal := naming.InstanceStsNum(instance, statefulSetName)
	serverID := ordinal + 1
	envs := []corev1.EnvVar{
		{
			Name:  "CLUSTER_ID",
			Value: naming.KDBInstanceClusterID(instance),
		},
		{
			Name:  "INSTANCE_NAME",
			Value: instance.Name,
		},
		{
			Name:  "INSTANCE_NAMESPACE",
			Value: instance.Namespace,
		},
		{
			Name: "NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{
				APIVersion: "v1",
				FieldPath:  "metadata.namespace",
			}},
		},
		{
			Name: "KDB_HOSTNAME",
			ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{
				APIVersion: "v1",
				FieldPath:  "metadata.name",
			}},
		},
		{
			Name:  "KDB_PORT",
			Value: fmt.Sprint(*instance.Spec.Port),
		},
		{
			Name:  "ENGINE_ENV",
			Value: naming.Engine(instance),
		},
		{
			Name:  "DEPLOY_ARCH",
			Value: naming.DeployArch(instance),
		},
		{
			Name: "KDB_POD_IP",
			ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{
				APIVersion: "v1",
				FieldPath:  "status.podIP",
			}},
		},
		{
			Name:  "KDB_CLUSTER_DOMAIN",
			Value: naming.ClusterDomain(),
		},
		{
			Name:  "MYSQL_SERVER_ID",
			Value: fmt.Sprint(serverID),
		},
		{
			Name: "ROLE",
			ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{
				APIVersion: "v1",
				FieldPath:  "metadata.labels['kdb.role']",
			}},
		},
		{
			Name:  "KDB_SIDECAR_LOG_DEBUG",
			Value: "true",
		},
	}
	mgrConfig, err := topology.ResolveMGRConfig(instance)
	if err != nil || !mgrConfig.Enabled {
		return envs
	}
	return append(envs, mgrEnvironment(instance, ordinal, mgrConfig)...)
}

func mgrEnvironment(instance *v1.KDBInstance, ordinal int, mgrConfig topology.MGRConfig) []corev1.EnvVar {
	bootstrap := "false"
	if int32(ordinal) == mgrConfig.BootstrapOrdinal {
		bootstrap = "true"
	}
	return []corev1.EnvVar{
		{Name: "ENABLE_MGR", Value: "1"},
		{Name: "MGR_MODE", Value: string(mgrConfig.Mode)},
		{Name: "MGR_GROUP_NAME", Value: mgrConfig.GroupName},
		{Name: "MGR_LOCAL_ADDRESS", Value: topology.BuildMGRLocalAddress(instance, ordinal, mgrConfig.GroupPort)},
		{Name: "MGR_SEEDS", Value: mgrConfig.Seeds},
		{Name: "MGR_GROUP_PORT", Value: fmt.Sprint(mgrConfig.GroupPort)},
		{Name: "MGR_BOOTSTRAP", Value: bootstrap},
		{Name: "MGR_BOOTSTRAP_ORDINAL", Value: fmt.Sprint(mgrConfig.BootstrapOrdinal)},
		{Name: "MGR_START_ON_BOOT", Value: "OFF"},
		{Name: "MGR_BOOTSTRAP_GROUP", Value: "OFF"},
	}
}
