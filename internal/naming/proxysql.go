package naming

import (
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	LabelComponent = "kdb.com/component"
	LabelProxyType = "kdb.com/proxy-type"

	ComponentProxySQL = "proxysql"

	ProxySQLConfigVolume  = "proxysql-config"
	ProxySQLSecretVolume  = "proxysql-secret"
	ProxySQLDataVolume    = "proxysql-data"
	ProxySQLRuntimeVolume = "proxysql-runtime"

	ProxySQLConfigMountPath  = "/etc/kdb/proxysql"
	ProxySQLSecretMountPath  = "/etc/kdb/proxysql-secret"
	ProxySQLDataMountPath    = "/var/lib/proxysql"
	ProxySQLRuntimeMountPath = "/var/run/kdb/proxysql"

	ProxySQLConfigFileName        = "proxysql.cnf"
	ProxySQLDesiredFileName       = "desired.yaml"
	ProxySQLConfigVersionFileName = "config-version"

	ProxySQLWriterHostgroup  int32 = 10
	ProxySQLReaderHostgroup  int32 = 20
	ProxySQLOfflineHostgroup int32 = 90
)

func ProxySQLLabels(cluster *v1.KDBCluster) map[string]string {
	return map[string]string{
		LabelClusterID: cluster.Name,
		LabelInstance:  cluster.Name,
		LabelComponent: ComponentProxySQL,
		LabelProxyType: "proxysql",
	}
}

func ProxySQLConfigMap(cluster *v1.KDBCluster) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Namespace: cluster.Namespace,
		Name:      cluster.Name + "-proxysql-config",
	}
}

func ProxySQLSecret(cluster *v1.KDBCluster) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Namespace: cluster.Namespace,
		Name:      cluster.Name + "-proxysql-secret",
	}
}

func ProxySQLDeployment(cluster *v1.KDBCluster) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Namespace: cluster.Namespace,
		Name:      cluster.Name + "-proxysql",
	}
}

func ProxySQLService(cluster *v1.KDBCluster) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Namespace: cluster.Namespace,
		Name:      cluster.Name + "-proxy",
	}
}
