package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	MonitoringStackPhasePending     = "Pending"
	MonitoringStackPhaseProgressing = "Progressing"
	MonitoringStackPhaseReady       = "Ready"
	MonitoringStackPhaseFailed      = "Failed"
)

type MonitoringStackPrometheusSpec struct {
	// Replicas controls Prometheus replica count.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// Retention controls Prometheus TSDB retention.
	// +optional
	// +kubebuilder:default="15d"
	Retention string `json:"retention,omitempty"`

	// ExternalLabels are attached to every Prometheus series so a central
	// backend can distinguish Cells without relying on cluster-local labels.
	// Values are validated by the monitoring-stack controller.
	// +optional
	ExternalLabels map[string]string `json:"externalLabels,omitempty"`

	// RemoteWrite forwards metrics to an explicitly configured endpoint. The
	// bearer token, when needed, is read from a Secret in the monitoring
	// namespace; raw credentials are never accepted in this CRD.
	// +optional
	// +kubebuilder:validation:MaxItems=4
	RemoteWrite []MonitoringStackRemoteWriteSpec `json:"remoteWrite,omitempty"`

	// Resources controls Prometheus container requests and limits.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

type MonitoringStackRemoteWriteSpec struct {
	// URL is the HTTPS/HTTP remote-write endpoint.
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// AuthorizationSecretRef points to a bearer token Secret key in the
	// monitoring namespace. The Secret itself is managed separately.
	// +optional
	AuthorizationSecretRef *corev1.SecretKeySelector `json:"authorizationSecretRef,omitempty"`
}

type MonitoringStackAlertmanagerSpec struct {
	// Replicas controls Alertmanager replica count.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// Resources controls Alertmanager container requests and limits.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

type MonitoringStackAlertRelaySpec struct {
	// Enabled controls the managed Alertmanager -> Relay -> center path. It is
	// false by default so existing monitoring stacks remain unchanged.
	// +optional
	// +kubebuilder:default=false
	Enabled *bool `json:"enabled,omitempty"`

	// Image is the immutable image containing /kdb/bin/alert-relay.
	// Required when Enabled is true.
	// +optional
	Image string `json:"image,omitempty"`

	// CellID is the cloud-cluster identity encoded in the Relay mTLS certificate.
	// +optional
	CellID string `json:"cellId,omitempty"`

	// CenterEndpointSecretRef points to the center base URL. Raw endpoints and
	// credentials are not embedded in the CR or generated manifests.
	// +optional
	CenterEndpointSecretRef *corev1.SecretKeySelector `json:"centerEndpointSecretRef,omitempty"`

	// TLSSecretRef points to a Secret containing tls.crt, tls.key and ca.crt.
	// +optional
	TLSSecretRef *corev1.SecretReference `json:"tlsSecretRef,omitempty"`

	// FallbackSecretRef is reserved for the gated T10 Metric P0 fallback path.
	// T05 mounts or reads no fallback credential.
	// +optional
	FallbackSecretRef *corev1.SecretKeySelector `json:"fallbackSecretRef,omitempty"`

	// Resources controls Relay container requests and limits.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

type MonitoringStackGrafanaSpec struct {
	// Enabled controls whether the operator deploys the built-in Grafana.
	// +optional
	// +kubebuilder:default=true
	Enabled *bool `json:"enabled,omitempty"`

	// Image is the Grafana image used by the built-in Grafana deployment.
	// +optional
	Image string `json:"image,omitempty"`

	// Resources controls Grafana container requests and limits.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// KDBMonitoringStackSpec defines the desired monitoring stack managed by kdb-operator.
type KDBMonitoringStackSpec struct {
	// Namespace is where Prometheus Operator runtime components, Prometheus,
	// Alertmanager, and Grafana are deployed.
	// +optional
	// +kubebuilder:default="kdb-observability"
	Namespace string `json:"namespace,omitempty"`

	// OperatorVersion records the Prometheus Operator version expected by this stack.
	// +optional
	// +kubebuilder:default="v0.92.0"
	OperatorVersion string `json:"operatorVersion,omitempty"`

	// BundleURL points to an explicit Prometheus Operator bundle URL.
	// If empty, the operator uses the embedded offline bundle for OperatorVersion.
	// +optional
	BundleURL string `json:"bundleUrl,omitempty"`

	// Prometheus configures the built-in Prometheus CR.
	// +optional
	Prometheus MonitoringStackPrometheusSpec `json:"prometheus,omitempty"`

	// Alertmanager configures the built-in Alertmanager CR.
	// +optional
	Alertmanager MonitoringStackAlertmanagerSpec `json:"alertmanager,omitempty"`

	// Relay configures the optional Cell Alert Relay.
	// +optional
	Relay MonitoringStackAlertRelaySpec `json:"relay,omitempty"`

	// Grafana configures the built-in Grafana deployment.
	// +optional
	Grafana MonitoringStackGrafanaSpec `json:"grafana,omitempty"`
}

type MonitoringStackComponentStatus struct {
	// +optional
	Name string `json:"name,omitempty"`

	// +optional
	Kind string `json:"kind,omitempty"`

	// +optional
	Namespace string `json:"namespace,omitempty"`

	// +optional
	Status string `json:"status,omitempty"`

	// +optional
	Ready bool `json:"ready,omitempty"`

	// +optional
	Version string `json:"version,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`
}

// KDBMonitoringStackStatus defines observed monitoring stack state.
type KDBMonitoringStackStatus struct {
	// +optional
	Phase string `json:"phase,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	Ready bool `json:"ready,omitempty"`

	// +optional
	PrometheusURL string `json:"prometheusUrl,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`

	// +optional
	Components []MonitoringStackComponentStatus `json:"components,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="namespace",type="string",JSONPath=".spec.namespace"
// +kubebuilder:printcolumn:name="age",type="date",JSONPath=".metadata.creationTimestamp"
type KDBMonitoringStack struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KDBMonitoringStackSpec   `json:"spec,omitempty"`
	Status KDBMonitoringStackStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type KDBMonitoringStackList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KDBMonitoringStack `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KDBMonitoringStack{}, &KDBMonitoringStackList{})
}
