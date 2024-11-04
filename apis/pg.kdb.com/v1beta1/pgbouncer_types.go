package v1beta1

import (
	"github.com/sqc157400661/kdb/apis/shared"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const DefaultPGBPort = 5431

// PGBouncerConfiguration represents PgBouncer configuration files.
type PGBouncerConfiguration struct {

	// Files to mount under "/etc/pgbouncer". When specified, settings in the
	// "pgbouncer.ini" file are loaded before all others. From there, other
	// files may be included by absolute path. Changing these references causes
	// PgBouncer to restart, but changes to the file contents are automatically
	// reloaded.
	// More info: https://www.pgbouncer.org/config.html#include-directive
	// +optional
	Files []corev1.VolumeProjection `json:"files,omitempty"`

	// Settings that apply to the entire PgBouncer process.
	// More info: https://www.pgbouncer.org/config.html
	// +optional
	Global map[string]string `json:"global,omitempty"`

	// PgBouncer database definitions. The key is the database requested by a
	// client while the value is a libpq-styled connection string. The special
	// key "*" acts as a fallback. When this field is empty, PgBouncer is
	// configured with a single "*" entry that connects to the primary
	// PostgreSQL instance.
	// More info: https://www.pgbouncer.org/config.html#section-databases
	// +optional
	Databases map[string]string `json:"databases,omitempty"`

	// Connection settings specific to particular users.
	// More info: https://www.pgbouncer.org/config.html#section-users
	// +optional
	Users map[string]string `json:"users,omitempty"`
}

// PGBouncerPodSpec defines the desired state of a PgBouncer connection pooler.
type PGBouncerPodSpec struct {
	// +optional
	Metadata *shared.Metadata `json:"metadata,omitempty"`

	// Scheduling constraints of a PgBouncer pod. Changing this value causes
	// PgBouncer to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Configuration settings for the PgBouncer process. Changes to any of these
	// values will be automatically reloaded without validation. Be careful, as
	// you may put PgBouncer into an unusable state.
	// More info: https://www.pgbouncer.org/usage.html#reload
	// +optional
	Config PGBouncerConfiguration `json:"config,omitempty"`

	// Custom sidecars for a PgBouncer pod. Changing this value causes
	// PgBouncer to restart.
	// +optional
	Containers []corev1.Container `json:"containers,omitempty"`

	// Name of a container image that can run PgBouncer 1.15 or newer. Changing
	// this value causes PgBouncer to restart. The image may also be set using
	// the RELATED_IMAGE_PGBOUNCER environment variable.
	// More info: https://kubernetes.io/docs/concepts/containers/images
	// +optional
	Image string `json:"image,omitempty"`

	// Port on which PgBouncer should listen for client connections. Changing
	// this value causes PgBouncer to restart.
	// +optional
	// +kubebuilder:default=5431
	// +kubebuilder:validation:Minimum=1024
	Port *int32 `json:"port,omitempty"`

	// Priority class name for the pgBouncer pod. Changing this value causes
	// PostgreSQL to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/pod-priority-preemption/
	// +optional
	PriorityClassName *string `json:"priorityClassName,omitempty"`

	// Number of desired PgBouncer pods.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	Replicas *int32 `json:"replicas,omitempty"`

	// +optional
	DeployInSidecar *bool `json:"deployInSidecar,omitempty"`

	// RuntimeClassName refers to a RuntimeClass object in the node.k8s.io group, which should be used
	// to run this pod.  If no RuntimeClass resource matches the named class, the pod will not be run.
	// If unset or empty, the "legacy" RuntimeClass will be used, which is an implicit class with an
	// empty definition that uses the default runtime handler.
	// More info: https://git.k8s.io/enhancements/keps/sig-node/585-runtime-class
	// +optional
	RuntimeClassName *string `json:"runtimeClassName,omitempty"`

	// Minimum number of pods that should be available at a time.
	// Defaults to one when the replicas field is greater than one.
	// +optional
	MinAvailable *intstr.IntOrString `json:"minAvailable,omitempty"`

	// Compute resources of a PgBouncer container. Changing this value causes
	// PgBouncer to restart.
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Specification of the service that exposes PgBouncer.
	// +optional
	Service *shared.ServiceSpec `json:"service,omitempty"`

	// Tolerations of a PgBouncer pod. Changing this value causes PgBouncer to
	// restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Topology spread constraints of a PgBouncer pod. Changing this value causes
	// PgBouncer to restart.
	// More info: https://kubernetes.io/docs/concepts/workloads/pods/pod-topology-spread-constraints/
	// +optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
}

// Default returns the default port for PgBouncer (5431) if a port is not
// explicitly set
func (s *PGBouncerPodSpec) Default() {
	if s.Port == nil {
		s.Port = new(int32)
		*s.Port = DefaultPGBPort
	}

	if s.Replicas == nil {
		s.Replicas = new(int32)
		*s.Replicas = 1
	}

	if s.DeployInSidecar == nil {
		s.DeployInSidecar = new(bool)
		*s.DeployInSidecar = true
	}
}

type PGBouncerPodStatus struct {

	// Total number of ready pods.
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Total number of non-terminated pods.
	Replicas int32 `json:"replicas,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type PGBouncer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// NOTE: Every PostgresCluster needs a Spec, but it is optional here
	// so ObjectMeta can be managed independently.

	Spec   PGBouncerPodSpec   `json:"spec,omitempty"`
	Status PGBouncerPodStatus `json:"status,omitempty"`
}

func (c *PGBouncer) Default() {
	if len(c.APIVersion) == 0 {
		c.APIVersion = GroupVersion.String()
	}
	if len(c.Kind) == 0 {
		c.Kind = "PGBouncer"
	}
	c.Spec.Default()
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type PGBouncerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
}

func init() {
	SchemeBuilder.Register(&PGBouncer{}, &PGBouncerList{})
}
