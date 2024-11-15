package config

import (
	"os"
)

const K8SNamespaceEnv = "K8SNamespace"

var K8SNamespace string
var InitMySQLRole string

func init() {
	K8SNamespace = os.Getenv(K8SNamespaceEnv)
	if K8SNamespace == "" {
		K8SNamespace = "kdb"
	}
}

func IsIsNamespacePaused(namespace string) bool {
	return false
}
