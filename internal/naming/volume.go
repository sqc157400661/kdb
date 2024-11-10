package naming

import (
	corev1 "k8s.io/api/core/v1"
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

// ConfigVolumeMount returns the name and mount path of the kdb config volume.
func ConfigVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{Name: "kdb-config", MountPath: ConfigMountPath, ReadOnly: true}
}
