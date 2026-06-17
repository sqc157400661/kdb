package v1

import corev1 "k8s.io/api/core/v1"

const (
	PostgreSQLDCSKubernetes = "kubernetes"
	PostgreSQLDCSEtcd       = "etcd"
)

// PostgreSQLSpec contains PostgreSQL engine specific settings.
type PostgreSQLSpec struct {
	// Patroni configures PostgreSQL high availability runtime.
	// +optional
	Patroni *PostgreSQLPatroniSpec `json:"patroni,omitempty"`

	// Exporter configures the optional PostgreSQL exporter sidecar.
	// +optional
	Exporter *PostgreSQLExporterSpec `json:"exporter,omitempty"`

	// Parameters contains PostgreSQL settings rendered into Patroni dynamic configuration.
	// +optional
	Parameters map[string]string `json:"parameters,omitempty"`

	// HBA appends custom pg_hba.conf rules after operator-managed mandatory rules.
	// +optional
	HBA []string `json:"hba,omitempty"`
}

// PostgreSQLPatroniSpec contains Patroni runtime settings.
type PostgreSQLPatroniSpec struct {
	// DCS selects the distributed configuration store for Patroni.
	// +optional
	// +kubebuilder:default=kubernetes
	// +kubebuilder:validation:Enum=kubernetes;etcd
	DCS string `json:"dcs,omitempty"`

	// LeaderLeaseDurationSeconds is Patroni ttl.
	// +optional
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=10
	LeaderLeaseDurationSeconds *int32 `json:"leaderLeaseDurationSeconds,omitempty"`

	// SyncPeriodSeconds is Patroni loop_wait.
	// +optional
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	SyncPeriodSeconds *int32 `json:"syncPeriodSeconds,omitempty"`
}

// PostgreSQLExporterSpec contains PostgreSQL exporter sidecar settings.
type PostgreSQLExporterSpec struct {
	// Enabled controls whether the operator injects the PostgreSQL exporter container.
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

// PostgreSQLStatus contains observed PostgreSQL runtime state.
type PostgreSQLStatus struct {
	// Primary is the current primary Pod name when known.
	// +optional
	Primary string `json:"primary,omitempty"`

	// Replicas contains known replica Pod names.
	// +optional
	Replicas []string `json:"replicas,omitempty"`

	// Ready indicates whether at least one primary endpoint is available and all expected pods are ready.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// Endpoints contains ready PostgreSQL pod network endpoints.
	// +optional
	Endpoints []HostInfo `json:"endpoints,omitempty"`
}
