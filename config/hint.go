package config

import (
	"os"
	"strings"
)

// IsNamespacePaused determine is the cluster can continue to Reconcile based on this configuration
// todo realize
func IsNamespacePaused(ns string) bool {
	if os.Getenv(strings.ToUpper(ns)) == "paused" {
		return true
	}
	return false
}

// IsFedEnable is the federated cluster capability been enabled
func IsFedEnable() bool {
	return false
}

// PGONamespace returns the namespace where the PGO is running,
// based on the env var from the DownwardAPI
// If no env var is found, returns ""
func PGONamespace() string {
	return os.Getenv("PGO_NAMESPACE")
}
