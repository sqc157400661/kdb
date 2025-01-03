package naming

import v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"

const annoPrefix = "kdb."
const (
	MySQLPortAnno = annoPrefix + "mysqlPort"

	// StopReconcile the cluster stop reconcile.
	StopReconcile = annoPrefix + "stop-reconcile"

	// CurrentInstanceConfigVersion the current config version of the KDBInstance Sidecar
	CurrentInstanceConfigVersion = annoPrefix + "current-instance-config-version"
	// UpdateInstanceConfigVersion the config version to be updated for the KDBInstance Sidecar
	UpdateInstanceConfigVersion = annoPrefix + "update-instance-config-version"
)

// CurrentConfigVersion return current config version of the KDBInstance Sidecar .
func CurrentConfigVersion(instance *v1.KDBInstance) string {
	if instance.Annotations != nil {
		return instance.Annotations[CurrentInstanceConfigVersion]
	}
	return ""
}

// UpdateConfigVersion return updating config version of the KDBInstance Sidecar .
func UpdateConfigVersion(instance *v1.KDBInstance) string {
	if instance.Annotations != nil {
		return instance.Annotations[UpdateInstanceConfigVersion]
	}
	return ""
}
