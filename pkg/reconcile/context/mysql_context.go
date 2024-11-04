package reconcile_context

import (
	"github.com/sqc157400661/helper/kube"
	corev1 "k8s.io/api/core/v1"
)

type MySQLContext struct {
	// base reconcileContext
	kube.ReconcileContext
	// config
	globalConfig map[string][]byte

	clusterConfigMap *corev1.ConfigMap
}

func NewMySQLContext(base kube.ReconcileContext) *MySQLContext {
	return &MySQLContext{
		ReconcileContext: base,
	}
}
