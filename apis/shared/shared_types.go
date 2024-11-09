package shared

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
)

// SchemalessObject is a map compatible with JSON object.
//
// Use with the following markers:
// - kubebuilder:pruning:PreserveUnknownFields
// - kubebuilder:validation:Schemaless
// - kubebuilder:validation:Type=object
type SchemalessObject map[string]interface{}

// DeepCopy creates a new SchemalessObject by copying the receiver.
func (in *SchemalessObject) DeepCopy() *SchemalessObject {
	if in == nil {
		return nil
	}
	out := new(SchemalessObject)
	*out = runtime.DeepCopyJSON(*in)
	return out
}

type ServiceSpec struct {
	// +optional
	Metadata *Metadata `json:"metadata,omitempty"`

	// The port on which this service is exposed when type is NodePort or
	// LoadBalancer. Value must be in-range and not in use or the operation will
	// fail. If unspecified, a port will be allocated if this Service requires one.
	// - https://kubernetes.io/docs/concepts/services-networking/service/#type-nodeport
	// +optional
	NodePort *int32 `json:"nodePort,omitempty"`

	// More info: https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services-service-types
	//
	// +optional
	// +kubebuilder:default=ClusterIP
	// +kubebuilder:validation:Enum={ClusterIP,NodePort,LoadBalancer}
	Type string `json:"type"`
}

type InstanceSetSpec struct {
	// +optional
	Metadata *Metadata `json:"metadata,omitempty"`

	// Number of desired KDB db pods.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// RuntimeClassName refers to a RuntimeClass object in the node.k8s.io group, which should be used
	// to run this pod.  If no RuntimeClass resource matches the named class, the pod will not be run.
	// If unset or empty, the "legacy" RuntimeClass will be used, which is an implicit class with an
	// empty definition that uses the default runtime handler.
	// More info: https://git.k8s.io/enhancements/keps/sig-node/585-runtime-class
	// +optional
	RuntimeClassName *string `json:"runtimeClassName,omitempty"`

	// Priority class name for the KDB pod. Changing this value causes
	// KDB pod to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/pod-priority-preemption/
	// +optional
	PriorityClassName *string `json:"priorityClassName,omitempty"`

	// Scheduling constraints of a KDB pod. Changing this value causes
	// instance to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Tolerations of a KDB pod. Changing this value causes KDB pod to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// pod initcontainer
	// +optional
	InitContainer ContainerSpec `json:"initContainer"`

	// pod mysqld container
	// +optional
	MainContainer ContainerSpec `json:"mainContainer"`

	// pod sidecar container
	// +optional
	SidecarContainer ContainerSpec `json:"sidecarContainer"`

	// The specification of monitoring tools that connect to KDB
	// +optional
	MonitorContainer ContainerSpec `json:"monitoring,omitempty"`

	// Defines a PersistentVolumeClaim for KDB db data.
	// More info: https://kubernetes.io/docs/concepts/storage/persistent-volumes
	// +kubebuilder:validation:Required
	DataVolumeClaimSpec PVCSpec `json:"dataVolumeClaimSpec"`

	// Defines a separate PersistentVolumeClaim for KDB's write-ahead log.
	// ep.More info: https://www.postgresql.org/docs/current/wal.html
	// +optional
	LogVolumeClaimSpec *PVCSpec `json:"logVolumeClaimSpec,omitempty"`

	// Topology spread constraints of a KDB pod. Changing this value causes
	// KDB pod to restart.
	// More info: https://kubernetes.io/docs/concepts/workloads/pods/pod-topology-spread-constraints/
	// +optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
}

const (
	InstanceStatusRunning = "Running"
	InstanceStatusPending = "Pending"
	InstanceStatusFailed  = "Failed"
)

// InstanceSetStatus instance status
// +kubebuilder:validation:Enum=Running;Pending;Failed
type InstanceSetStatus struct {
	// Total number of ready pods.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Total number of pods.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Total number of pods that have the desired specification.
	// +optional
	UpdatedReplicas int32 `json:"updatedReplicas,omitempty"`

	// PodInfos contains information about the working pod of mysql instance
	// +optional
	PodInfos []PodStatusInfo `json:"podInfos,omitempty"`
}

type PodStatusInfo struct {
	// +optional
	PodName string `json:"podName,omitempty"`

	// PodStatus
	// +optional
	PodPhase corev1.PodPhase `json:"podPhase,omitempty"`

	// +optional
	PodIP string `json:"podIP,omitempty"`

	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// +optional
	HostIP string `json:"hostIP,omitempty"`
}

// Default sets the default values for an instance set spec, including the name
// suffix and number of replicas.
func (s *InstanceSetSpec) Default(i int) {
	if s.Replicas == nil {
		s.Replicas = new(int32)
		*s.Replicas = 1
	}
}

// ContainerSpec defines the configuration of a  container
type ContainerSpec struct {
	// Name of the container specified as a DNS_LABEL.
	// Each container in a pod must have a unique name (DNS_LABEL).
	// Cannot be updated.
	Name string `json:"name"`

	// +optional
	Image string `json:"image"`

	// +optional
	Command []string `json:"command,omitempty"`

	// +optional
	Args []string `json:"args,omitempty"`

	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

type PVCSpec struct {
	// +optional
	Metadata *Metadata `json:"metadata,omitempty"`

	// +optional
	StorageClass string `json:"storageClass"`

	Size resource.Quantity `json:"size"`
}

// Metadata contains metadata for custom resources
type Metadata struct {
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// GetLabelsOrNil gets labels from a Metadata pointer, if Metadata
// hasn't been set return nil
func (meta *Metadata) GetLabelsOrNil() map[string]string {
	if meta == nil {
		return nil
	}
	return meta.Labels
}

// GetAnnotationsOrNil gets annotations from a Metadata pointer, if Metadata
// hasn't been set return nil
func (meta *Metadata) GetAnnotationsOrNil() map[string]string {
	if meta == nil {
		return nil
	}
	return meta.Annotations
}
