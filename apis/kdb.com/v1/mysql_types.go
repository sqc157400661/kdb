package v1

import corev1 "k8s.io/api/core/v1"

const (
	MySQLMGRModeSinglePrimary = "SinglePrimary"
	MySQLMGRModeMultiPrimary  = "MultiPrimary"
)

// MySQLSpec contains MySQL engine specific settings.
type MySQLSpec struct {
	// MGR configures MySQL Group Replication.
	// +optional
	MGR *MySQLMGRSpec `json:"mgr,omitempty"`

	// Exporter configures the optional mysqld_exporter sidecar.
	// +optional
	Exporter *MySQLExporterSpec `json:"exporter,omitempty"`
}

// MySQLExporterSpec contains mysqld_exporter sidecar settings.
type MySQLExporterSpec struct {
	// Enabled controls whether the operator injects the mysql-exporter container.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Image overrides spec.instance.monitoring.image when set.
	// +optional
	Image string `json:"image,omitempty"`

	// Env appends exporter-specific environment variables.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Resources overrides spec.instance.monitoring.resources when set.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// MySQLMGRSpec contains MySQL Group Replication settings.
type MySQLMGRSpec struct {
	// Mode controls whether MGR runs in single-primary or multi-primary mode.
	// +optional
	// +kubebuilder:default=SinglePrimary
	// +kubebuilder:validation:Enum=SinglePrimary;MultiPrimary
	Mode string `json:"mode,omitempty"`

	// GroupName is the MGR group UUID. If empty, operator generates a stable UUID from namespace/name.
	// +optional
	// +kubebuilder:validation:Pattern=`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
	GroupName string `json:"groupName,omitempty"`

	// BootstrapOrdinal is the only ordinal allowed to bootstrap a new group.
	// +optional
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	BootstrapOrdinal *int32 `json:"bootstrapOrdinal,omitempty"`

	// GroupPort is the MGR group communication port.
	// +optional
	// +kubebuilder:default=33061
	// +kubebuilder:validation:Minimum=1024
	// +kubebuilder:validation:Maximum=65535
	GroupPort *int32 `json:"groupPort,omitempty"`
}
