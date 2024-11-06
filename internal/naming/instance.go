package naming

import (
	"fmt"
	"github.com/hashicorp/go-version"
	v1 "github.com/sqc157400661/kdb/apis/mysql.kdb.com/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const GlobalConfigSecret = "kdb-global-config"

const (
	MasterRole  = "master"
	ReplicaRole = "replica"
)

const (
	MySQLEngine    = "mysql"
	PostgresEngine = "pg"
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
			LabelCluster: cluster,
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
			LabelCluster: KDBInstanceCluster(instance),
			LabelHaProxy: HaProxy(instance),
		},
	}
}

// HaProxy returns the "scope"  haproxy uses for instance.
func HaProxy(instance *v1.KDBInstance) string {
	return instance.Name + "-ha"
}

// KDBInstanceCluster return cluster id .
func KDBInstanceCluster(instance *v1.KDBInstance) string {
	if instance.Labels != nil {
		return instance.Labels[LabelCluster]
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

func IsMySQLEngine(instance *v1.KDBInstance) bool {
	if instance.Spec.Engine == MySQLEngine {
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
