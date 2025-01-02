package naming

import (
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KDBCluster selects things for a single clusterName in a cluster.
func KDBCluster(clusterName string) metav1.LabelSelector {
	return metav1.LabelSelector{
		MatchLabels: map[string]string{
			LabelInstance: clusterName,
		},
	}
}

func IsMasterSlaveCluster(cluster *v1.KDBCluster) bool {
	return IsMasterSlaveArch(cluster.Spec.DeployArch)
}

// IsMasterSlaveArch is master slave architecture
func IsMasterSlaveArch(t string) bool {
	if util.InStringSlice([]string{
		MySQLMasterSlaveDeployArch,
		MySQLMasterReplicaDeployArch,
	}, t) {
		return true
	}
	return false
}
