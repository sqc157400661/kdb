package generate

import (
	"fmt"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
)

// RequestEnvironment returns the environment variables required to invoke kdb utilities.
func RequestEnvironment(instance *v1.KDBInstance) []corev1.EnvVar {
	return []corev1.EnvVar{
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
	}
}
