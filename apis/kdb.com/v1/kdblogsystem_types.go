package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	LogSystemPhasePending     = "Pending"
	LogSystemPhaseProgressing = "Progressing"
	LogSystemPhaseReady       = "Ready"
	LogSystemPhaseFailed      = "Failed"
)

type LogSystemEndpointSpec struct {
	// Write is the endpoint used by collectors or adapters to write logs.
	// +optional
	Write string `json:"write,omitempty"`

	// Query is the endpoint used by KDB console query adapters.
	// +optional
	Query string `json:"query,omitempty"`

	// Console is an optional external console URL.
	// +optional
	Console string `json:"console,omitempty"`
}

type LogSystemCollectorSpec struct {
	// Namespace is where the Fluent Bit DaemonSet should run.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// DaemonSet is the collector DaemonSet name.
	// +optional
	DaemonSet string `json:"daemonSet,omitempty"`

	// Image is the collector image expected by this log system.
	// +optional
	Image string `json:"image,omitempty"`

	// ExtraLogDirs are additional in-container directories collected as *.log files.
	// System preset directories are always kept and these directories are appended.
	// +optional
	ExtraLogDirs []string `json:"extraLogDirs,omitempty"`

	// ConfigPatch is serialized into the managed ConfigMap for the executor/observer.
	// +optional
	ConfigPatch map[string]string `json:"configPatch,omitempty"`
}

type LogSystemGatewaySpec struct {
	// Image is the custom log gateway or adapter image managed by the operator.
	// +optional
	Image string `json:"image,omitempty"`

	// Replicas controls the gateway deployment replica count.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// Resources controls gateway container requests and limits.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// KDBLogSystemSpec defines a self-built/custom log system managed by the operator.
type KDBLogSystemSpec struct {
	// BackendID links the Kubernetes object to the KDB admin log backend record.
	// +kubebuilder:validation:Required
	BackendID string `json:"backendId"`

	// BackendType describes the write/query protocol.
	// +kubebuilder:validation:Enum=custom_http;loki;opensearch;clickhouse;kafka_clickhouse
	// +kubebuilder:default=custom_http
	BackendType string `json:"backendType,omitempty"`

	// LoggingAppCode is the KDB logging app uniqueness key.
	// +optional
	LoggingAppCode string `json:"loggingAppCode,omitempty"`

	// RetentionDays records the expected retention policy for downstream validation.
	// +optional
	RetentionDays int32 `json:"retentionDays,omitempty"`

	// Endpoints are non-secret connection endpoints.
	// +optional
	Endpoints LogSystemEndpointSpec `json:"endpoints,omitempty"`

	// CredentialSecretRef references a secret with write/query credentials.
	// +optional
	CredentialSecretRef *corev1.LocalObjectReference `json:"credentialSecretRef,omitempty"`

	// Collector describes the node-level collector expected for this log system.
	// +optional
	Collector LogSystemCollectorSpec `json:"collector,omitempty"`

	// Gateway describes the custom gateway/adapter managed by the operator.
	// +optional
	Gateway LogSystemGatewaySpec `json:"gateway,omitempty"`
}

// KDBLogSystemStatus defines the observed state of a self-built/custom log system.
type KDBLogSystemStatus struct {
	// +optional
	Phase string `json:"phase,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// +optional
	ConfigHash string `json:"configHash,omitempty"`

	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="backend",type="string",JSONPath=".spec.backendId"
// +kubebuilder:printcolumn:name="type",type="string",JSONPath=".spec.backendType"
// +kubebuilder:printcolumn:name="phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="age",type="date",JSONPath=".metadata.creationTimestamp"
type KDBLogSystem struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KDBLogSystemSpec   `json:"spec,omitempty"`
	Status KDBLogSystemStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type KDBLogSystemList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KDBLogSystem `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KDBLogSystem{}, &KDBLogSystemList{})
}
