package naming

import (
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
