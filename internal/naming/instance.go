package naming

import (
	"fmt"
	"github.com/hashicorp/go-version"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"strings"
)

const GlobalConfigSecret = "kdb-global-config"
const GlobalConfigSecretKey = "global"

const (
	MasterRole  = "master"
	ReplicaRole = "replica"
)

const (
	MySQLEngine    = "mysql"
	PostgresEngine = "pg"
)

const (
	MySQLSingleDeployArch        = "Single"
	MySQLMasterSlave01DeployArch = "Master-Slave-01"
	MySQLMasterSlave02DeployArch = "Master-Slave-02"
	MySQLMGRDeployArch           = "MGR"
)

// AsSelector is a wrapper around metav1.LabelSelectorAsSelector() which converts
// the LabelSelector API type into something that implements labels.Selector.
func AsSelector(s metav1.LabelSelector) (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(&s)
}

// KDBInstances selects things for KDB instances in one cluster.
func KDBInstances(cluster string) metav1.LabelSelector {
	return metav1.LabelSelector{
		MatchLabels: map[string]string{
			LabelClusterID: cluster,
		},
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: LabelInstance, Operator: metav1.LabelSelectorOpExists},
		},
	}
}

// KDBInstance selects things for a single instance in a cluster.
func KDBInstance(instanceName string) metav1.LabelSelector {
	return metav1.LabelSelector{
		MatchLabels: map[string]string{
			LabelInstance: instanceName,
		},
	}
}

// KDBInstanceHaProxy selects things labeled for haProxy or sentinel in cluster.
func KDBInstanceHaProxy(instance *v1.KDBInstance) metav1.LabelSelector {
	return metav1.LabelSelector{
		MatchLabels: map[string]string{
			LabelClusterID: KDBInstanceClusterID(instance),
			LabelHaProxy:   HaProxy(instance),
		},
	}
}

// HaProxy returns the "scope"  haproxy uses for instance.
func HaProxy(instance *v1.KDBInstance) string {
	return instance.Name + "-ha"
}

// KDBInstanceClusterID return cluster id .
func KDBInstanceClusterID(instance *v1.KDBInstance) string {
	if instance.Labels != nil {
		return instance.Labels[LabelClusterID]
	}
	return ""
}

// KDBInstanceMasterHostname return master-slave master pod name .
func KDBInstanceMasterHostname(instance *v1.KDBInstance) string {
	if instance.Labels != nil {
		return instance.Labels[LabelMasterHostname]
	}
	return ""
}

// KDBInstanceMasterIp return master-slave master pod ip .
func KDBInstanceMasterIp(instance *v1.KDBInstance) string {
	if instance.Labels != nil {
		return instance.Labels[LabelMasterIP]
	}
	return ""
}

func Engine(instance *v1.KDBInstance) string {
	return instance.Spec.Engine
}

func IsMySQLEngine(instance *v1.KDBInstance) bool {
	if strings.ToLower(Engine(instance)) == MySQLEngine {
		return true
	}
	return false
}

func IsPGEngine(instance *v1.KDBInstance) bool {
	if strings.ToLower(Engine(instance)) == PostgresEngine {
		return true
	}
	return false
}

// EngineVersion return instance engine major version.
func EngineVersion(instance *v1.KDBInstance) (*version.Version, error) {
	return version.NewVersion(instance.Spec.EngineVersion)
}

// InstanceConfigMap returns the ObjectMeta necessary to lookup
// cluster's shared ConfigMap.
func InstanceConfigMap(instance *v1.KDBInstance) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Namespace: instance.Namespace,
		Name:      instance.Name + "-config",
	}
}

// InstanceRBAC returns the ObjectMeta necessary to lookup the
// ServiceAccount, Role, and RoleBinding for cluster's kdb instances.
func InstanceRBAC(instance *v1.KDBInstance) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Namespace: instance.Namespace,
		Name:      instance.Name + "-sa",
	}
}

func InstanceStatefulSetName(instanceSetName string, index int) string {
	return fmt.Sprintf("%s%d", instanceSetName, index)
}

// GenerateInstanceStatefulSetMeta returns a instance statefulSet meta.
func GenerateInstanceStatefulSetMeta(
	instance *v1.KDBInstance,
	index int,
) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Namespace: instance.Namespace,
		Name:      InstanceStatefulSetName(instance.Name, index),
	}
}

// InstanceDataVolume returns the ObjectMeta for the KDB data
// volume for instance.
func InstanceDataVolume(runner *appsv1.StatefulSet) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Namespace: runner.GetNamespace(),
		Name:      runner.GetName() + "-kdb-data",
	}
}

// InstanceLogVolume returns the ObjectMeta for the KDB log
// volume for instance.
func InstanceLogVolume(runner *appsv1.StatefulSet) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Namespace: runner.GetNamespace(),
		Name:      runner.GetName() + "-kdb-log",
	}
}
func InstanceSetSpec(instance *v1.KDBInstance) shared.InstanceSetSpec {
	return instance.Spec.InstanceSet
}

func InstanceDataPvcSpec(instance *v1.KDBInstance) shared.PVCSpec {
	return instance.Spec.InstanceSet.DataVolumeClaimSpec
}

func InstanceLogPvcSpec(instance *v1.KDBInstance) *shared.PVCSpec {
	return instance.Spec.InstanceSet.LogVolumeClaimSpec
}

func IsMasterPod(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	if len(pod.Labels) == 0 {
		return false
	}
	if pod.Labels[LabelRole] == MasterRole {
		return true
	}
	return false
}

func IsInstanceReady(instance *v1.KDBInstance) bool {
	if instance == nil {
		return false
	}

	if util.Int32(instance.Status.InstanceSet.Replicas) != instance.Spec.InstanceSet.Replicas {
		return false
	}

	if instance.Status.Conditions == nil {
		return false
	}

	return true
}

func IsMasterInstance(instance *v1.KDBInstance) bool {
	if instance == nil {
		return false
	}
	if len(instance.Labels) == 0 {
		return false
	}
	if instance.Labels[LabelRole] == MasterRole {
		return true
	}
	return false
}

// DeployArch return DeployArch.
func DeployArch(instance *v1.KDBInstance) string {
	if instance.Spec.DeployArch != "" {
		return instance.Spec.DeployArch
	}
	return ""
}
