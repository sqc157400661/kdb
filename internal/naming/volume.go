package naming

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	// DataMountPath is where to mount the main data volume.
	DataMountPath = "/kdbdata"

	// LogMountPath is where to mount the optional WAL volume.
	LogMountPath = "/kdblog"

	// DownwardAPIPath is where to mount the downwardAPI volume.
	DownwardAPIPath = "/etc/containerinfo"
)

// DataVolumeMount returns the name and mount path of the kdb data volume.
func DataVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{Name: "kdb-data", MountPath: DataMountPath}
}

// LogVolumeMount returns the name and mount path of the kdb WAL volume.
func LogVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{Name: "kdb-log", MountPath: LogMountPath}
}

// DownwardAPIVolumeMount returns the name and mount path of the DownwardAPI volume.
func DownwardAPIVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      "kdb-containerinfo",
		MountPath: DownwardAPIPath,
		ReadOnly:  true,
	}
}
