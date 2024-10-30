package v1beta1

import (
	"fmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const DefaultPGPort = 5432

// PostgresClusterSpec defines the desired state of PostgresCluster
type PostgresClusterSpec struct {
	// +optional
	Metadata *Metadata `json:"metadata,omitempty"`

	// Specifies a data source for bootstrapping the PostgreSQL cluster.
	// +optional
	DataSource *DataSource `json:"dataSource,omitempty"`

	// +optional
	PVLEnable bool `json:"pvlEnable,omitempty"`

	// +optional
	EtcdSecret *corev1.SecretProjection `json:"etcdSecret,omitempty"`

	// PostgreSQL backup configuration
	// +optional
	Backups Backups `json:"backups"`

	// The image name to use for PostgreSQL containers. When omitted, the value
	// comes from an operator environment variable. For standard PostgreSQL images,
	// the format is RELATED_IMAGE_POSTGRES_{postgresVersion},
	// e.g. RELATED_IMAGE_POSTGRES_13. For PostGIS enabled PostgreSQL images,
	// the format is RELATED_IMAGE_POSTGRES_{postgresVersion}_GIS_{postGISVersion},
	// e.g. RELATED_IMAGE_POSTGRES_13_GIS_3.1.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,order=1
	Image string `json:"image,omitempty"`

	// ImagePullPolicy is used to determine when Kubernetes will attempt to
	// pull (download) container images.
	// More info: https://kubernetes.io/docs/concepts/containers/images/#image-pull-policy
	// +kubebuilder:validation:Enum={Always,Never,IfNotPresent}
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// The image pull secrets used to pull from a private registry
	// Changing this value causes all running pods to restart.
	// https://k8s.io/docs/tasks/configure-pod-container/pull-image-private-registry/
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Specifies one or more sets of PostgreSQL pods that replicate data for
	// this cluster.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +operator-sdk:csv:customresourcedefinitions:type=spec,order=2
	InstanceSets []PostgresInstanceSetSpec `json:"instances"`

	// +optional
	Patroni *PatroniSpec `json:"patroni,omitempty"`

	// Suspends the rollout and reconciliation of changes made to the
	// PostgresCluster spec.
	// +optional
	Paused *bool `json:"paused,omitempty"`

	// The port on which PostgreSQL should listen.
	// +optional
	// +kubebuilder:default=5432
	// +kubebuilder:validation:Minimum=1024
	Port *int32 `json:"port,omitempty"`

	// The major version of PostgreSQL installed in the PostgreSQL image
	// +kubebuilder:validation:Required
	PostgresVersion int `json:"postgresVersion"`

	// The full version of PostgreSQL installed in the PostgreSQL image
	// +optional
	PostgresFullVersion string `json:"postgresFullVersion"`

	// The PostGIS extension version installed in the PostgreSQL image.
	// When image is not set, indicates a PostGIS enabled image will be used.
	// +optional
	PostGISVersion string `json:"postGISVersion,omitempty"`

	// The specification of a proxy that connects to PostgreSQL.
	// +optional
	Proxy *PostgresProxySpec `json:"proxy,omitempty"`

	// The specification of a user interface that connects to PostgreSQL.
	// +optional
	UserInterface *UserInterfaceSpec `json:"userInterface,omitempty"`

	// The specification of monitoring tools that connect to PostgreSQL
	// +optional
	Monitoring *MonitoringSpec `json:"monitoring,omitempty"`

	// Specification of the service that exposes the PostgreSQL primary instance.
	// +optional
	Service *ServiceSpec `json:"service,omitempty"`

	// Whether or not the PostgreSQL cluster should be stopped.
	// When this is true, workloads are scaled to zero and CronJobs
	// are suspended.
	// Other resources, such as Services and Volumes, remain in place.
	// +optional
	Shutdown *bool `json:"shutdown,omitempty"`

	// A list of group IDs applied to the process of a container. These can be
	// useful when accessing shared file systems with constrained permissions.
	// More info: https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#security-context
	// +optional
	SupplementalGroups []int64 `json:"supplementalGroups,omitempty"`

	// Users to create inside PostgreSQL and the databases they should access.
	// The default creates one user that can access one database matching the
	// PostgresCluster name. An empty list creates no users. Removing a user
	// from this list does NOT drop the user nor revoke their access.
	// +listType=map
	// +listMapKey=name
	// +optional
	Users []PostgresUserSpec `json:"users,omitempty"`

	Config PostgresAdditionalConfig `json:"config,omitempty"`
}

// MonitoringSpec is a union of the supported PostgreSQL Monitoring tools
type MonitoringSpec struct {
	// +optional
	PGMonitor *PGMonitorSpec `json:"pgmonitor,omitempty"`
}

// MonitoringStatus is the current state of PostgreSQL cluster monitoring tool
// configuration
type MonitoringStatus struct {
	// +optional
	ExporterConfiguration string `json:"exporterConfiguration,omitempty"`
}

// PGMonitorSpec defines the desired state of the pgMonitor tool suite
type PGMonitorSpec struct {
	// +optional
	Exporter *ExporterSpec `json:"exporter,omitempty"`
}

type ExporterSpec struct {

	// The image name to use for postgres-exporter containers. The image may
	// also be set using the RELATED_IMAGE_PGEXPORTER environment variable.
	// +optional
	Image string `json:"image,omitempty"`

	// Changing this value causes PostgreSQL and the exporter to restart.
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// DataSource defines data sources for a new PostgresCluster.
type DataSource struct {
	// Defines a pgBackRest data source that can be used to pre-populate the PostgreSQL data
	// directory for a new PostgreSQL cluster using a pgBackRest restore.
	// The PGBackRest field is incompatible with the PostgresCluster field: only one
	// data source can be used for pre-populating a new PostgreSQL cluster
	// +optional
	PostgresCluster *PostgresClusterDataSource `json:"postgresCluster,omitempty"`

	// Defines any existing volumes to reuse for this PostgresCluster.
	// +optional
	Volumes *DataSourceVolumes `json:"volumes,omitempty"`
}

// DataSourceVolumes defines any existing volumes to reuse for this PostgresCluster.
type DataSourceVolumes struct {
	// Defines the existing pgData volume and directory to use in the current
	// PostgresCluster.
	// +optional
	PGDataVolume *DataSourceVolume `json:"pgDataVolume,omitempty"`

	// Defines the existing pg_wal volume and directory to use in the current
	// PostgresCluster. Note that a defined pg_wal volume MUST be accompanied by
	// a pgData volume.
	// +optional
	PGWALVolume *DataSourceVolume `json:"pgWALVolume,omitempty"`

	// Defines the existing pgBackRest repo volume and directory to use in the
	// current PostgresCluster.
	// +optional
	PGBackRestVolume *DataSourceVolume `json:"pgBackRestVolume,omitempty"`
}

// DataSourceVolume defines the PVC name and data diretory path for an existing cluster volume.
type DataSourceVolume struct {
	// The existing PVC name.
	PVCName string `json:"pvcName"`

	// The existing directory. When not set, a move Job is not created for the
	// associated volume.
	// +optional
	Directory string `json:"directory,omitempty"`
}

// PostgresClusterDataSource defines a data source for bootstrapping PostgreSQL clusters using a
// an existing PostgresCluster.
type PostgresClusterDataSource struct {

	// The name of an existing PostgresCluster to use as the data source for the new PostgresCluster.
	// Defaults to the name of the PostgresCluster being created if not provided.
	// +optional
	ClusterName string `json:"clusterName,omitempty"`

	// The namespace of the cluster specified as the data source using the clusterName field.
	// Defaults to the namespace of the PostgresCluster being created if not provided.
	// +optional
	ClusterNamespace string `json:"clusterNamespace,omitempty"`

	// The name of the pgBackRest repo within the source PostgresCluster that contains the backups
	// that should be utilized to perform a pgBackRest restore when initializing the data source
	// for the new PostgresCluster.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=^repo[1-4]
	RepoName string `json:"repoName"`

	// Command line options to include when running the pgBackRest restore command.
	// https://pgbackrest.org/command.html#command-restore
	// +optional
	Options []string `json:"options,omitempty"`

	// Resource requirements for the pgBackRest restore Job.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Scheduling constraints of the pgBackRest restore Job.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Priority class name for the pgBackRest restore Job pod. Changing this
	// value causes PostgreSQL to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/pod-priority-preemption/
	// +optional
	PriorityClassName *string `json:"priorityClassName,omitempty"`

	// Tolerations of the pgBackRest restore Job.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

// Default defines several key default values for a Postgres cluster.
func (s *PostgresClusterSpec) Default() {
	for i := range s.InstanceSets {
		s.InstanceSets[i].Default(i)
	}

	if s.Patroni == nil {
		s.Patroni = new(PatroniSpec)
	}
	s.Patroni.Default()

	if s.Port == nil {
		s.Port = new(int32)
		*s.Port = DefaultPGPort
	}

	if s.Proxy != nil {
		s.Proxy.Default()
	}

	if s.UserInterface != nil {
		s.UserInterface.Default()
	}
}

// Backups defines a PostgreSQL archive configuration
type Backups struct {

	// pgBackRest archive configuration
	// +optional
	PGBackRest PGBackRestArchive `json:"pgbackrest"`
}

// PostgresClusterStatus defines the observed state of PostgresCluster
type PostgresClusterStatus struct {
	// Current state of PostgreSQL instances.
	// +listType=map
	// +listMapKey=name
	// +optional
	InstanceSets []PostgresInstanceSetStatus `json:"instances,omitempty"`

	// +optional
	RSAddr []RSAddrStatus `json:"rsAddr,omitempty"`

	// +optional
	Patroni PatroniStatus `json:"patroni,omitempty"`

	// Status information for pgBackRest
	// +optional
	PGBackRest *PGBackRestStatus `json:"pgbackrest,omitempty"`

	// Current state of the PostgreSQL proxy.
	// +optional
	Proxy PostgresProxyStatus `json:"proxy,omitempty"`

	// The instance that should be started first when bootstrapping and/or starting a
	// PostgresCluster.
	// +optional
	StartupInstance string `json:"startupInstance,omitempty"`

	// The instance set associated with the startupInstance
	// +optional
	StartupInstanceSet string `json:"startupInstanceSet,omitempty"`

	// Current state of the PostgreSQL user interface.
	// +optional
	UserInterface *PostgresUserInterfaceStatus `json:"userInterface,omitempty"`

	// Current state of PostgreSQL cluster monitoring tool configuration
	// +optional
	Monitoring MonitoringStatus `json:"monitoring,omitempty"`

	// observedGeneration represents the .metadata.generation on which the status was based.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the observations of postgrescluster's current state.
	// Known .status.conditions.type are: "PersistentVolumeResizing",
	// "Progressing", "ProxyAvailable"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +operator-sdk:csv:customresourcedefinitions:type=status,xDescriptors={"urn:alm:descriptor:io.kubernetes.conditions"}
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PostgresClusterStatus condition types.
const (
	PersistentVolumeResizing   = "PersistentVolumeResizing"
	PostgresClusterProgressing = "Progressing"
	ProxyAvailable             = "ProxyAvailable"
)

type PostgresInstanceSetSpec struct {
	// +optional
	Metadata *Metadata `json:"metadata,omitempty"`

	// This value goes into the name of an appsv1.StatefulSet, the hostname of
	// a corev1.Pod, and label values. The pattern below is IsDNS1123Label
	// wrapped in "()?" to accommodate the empty default.
	//
	// The Pods created by a StatefulSet have a "controller-revision-hash" label
	// comprised of the StatefulSet name, a dash, and a 10-character hash.
	// The length below is derived from limitations on label values:
	//
	//   63 (max) ≥ len(cluster) + 1 (dash)
	//                + len(set) + 1 (dash) + 4 (id)
	//                + 1 (dash) + 10 (hash)
	//
	// See: https://issue.k8s.io/64023

	// Name that associates this set of PostgreSQL pods. This field is optional
	// when only one instance set is defined. Each instance set in a cluster
	// must have a unique name. The combined length of this and the cluster name
	// must be 46 characters or less.
	// +optional
	// +kubebuilder:default=""
	// +kubebuilder:validation:Pattern=`^([a-z0-9]([-a-z0-9]*[a-z0-9])?)?$`
	Name string `json:"name"`

	// +optional
	K8SCluster []string `json:"k8s_cluster,omitempty"`
	// Scheduling constraints of a PostgreSQL pod. Changing this value causes
	// PostgreSQL to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Custom sidecars for PostgreSQL instance pods. Changing this value causes
	// PostgreSQL to restart.
	// +optional
	Containers []corev1.Container `json:"containers,omitempty"`

	// Defines a PersistentVolumeClaim for PostgreSQL data.
	// More info: https://kubernetes.io/docs/concepts/storage/persistent-volumes
	// +kubebuilder:validation:Required
	DataVolumeClaimSpec corev1.PersistentVolumeClaimSpec `json:"dataVolumeClaimSpec"`

	// Priority class name for the PostgreSQL pod. Changing this value causes
	// PostgreSQL to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/pod-priority-preemption/
	// +optional
	PriorityClassName *string `json:"priorityClassName,omitempty"`

	// Number of desired PostgreSQL pods.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// Compute resources of a PostgreSQL container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Configuration for instance sidecar containers
	// +optional
	Sidecars *InstanceSidecars `json:"sidecars,omitempty"`

	// Tolerations of a PostgreSQL pod. Changing this value causes PostgreSQL to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Topology spread constraints of a PostgreSQL pod. Changing this value causes
	// PostgreSQL to restart.
	// More info: https://kubernetes.io/docs/concepts/workloads/pods/pod-topology-spread-constraints/
	// +optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`

	// Defines a separate PersistentVolumeClaim for PostgreSQL's write-ahead log.
	// More info: https://www.postgresql.org/docs/current/wal.html
	// +optional
	WALVolumeClaimSpec *corev1.PersistentVolumeClaimSpec `json:"walVolumeClaimSpec,omitempty"`

	// RuntimeClassName refers to a RuntimeClass object in the node.k8s.io group, which should be used
	// to run this pod.  If no RuntimeClass resource matches the named class, the pod will not be run.
	// If unset or empty, the "legacy" RuntimeClass will be used, which is an implicit class with an
	// empty definition that uses the default runtime handler.
	// More info: https://git.k8s.io/enhancements/keps/sig-node/585-runtime-class
	// +optional
	RuntimeClassName *string `json:"runtimeClassName,omitempty"`

	// HostAliases is an optional list of hosts and IPs that will be injected into the pod's hosts
	// file if specified. This is only valid for non-hostNetwork pods.
	// +optional
	HostAliases []corev1.HostAlias `json:"hostAliases,omitempty"`
}

// InstanceSidecars defines the configuration for instance sidecar containers
type InstanceSidecars struct {
	// Defines the configuration for the replica cert copy sidecar container
	// +optional
	ReplicaCertCopy *Sidecar `json:"replicaCertCopy,omitempty"`
}

type ETCD struct {
	// +optional
	Host string `json:"host,omitempty"`

	// +optional
	Secret string `json:"secret,omitempty"`
}

// Default sets the default values for an instance set spec, including the name
// suffix and number of replicas.
func (s *PostgresInstanceSetSpec) Default(i int) {
	if s.Name == "" {
		s.Name = fmt.Sprintf("%02d", i)
	}
	if s.Replicas == nil {
		s.Replicas = new(int32)
		*s.Replicas = 1
	}
}

type PostgresInstanceSetStatus struct {
	Name string `json:"name"`

	// Total number of ready pods.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Total number of pods.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Total number of pods that have the desired specification.
	// +optional
	UpdatedReplicas int32 `json:"updatedReplicas,omitempty"`
}

// PostgresProxySpec is a union of the supported PostgreSQL proxies.
type PostgresProxySpec struct {

	// Defines a PgBouncer proxy and connection pooler.
	PGBouncer *PGBouncerPodSpec `json:"pgBouncer"`
}

// Default sets the defaults for any proxies that are set.
func (s *PostgresProxySpec) Default() {
	if s.PGBouncer != nil {
		s.PGBouncer.Default()
	}
}

type PostgresProxyStatus struct {
	PGBouncer PGBouncerPodStatus `json:"pgBouncer,omitempty"`
}

// UserInterfaceSpec is a union of the supported PostgreSQL user interfaces.
type UserInterfaceSpec struct {

	// Defines a pgAdmin user interface.
	PGAdmin *PGAdminPodSpec `json:"pgAdmin"`
}

// Default sets the defaults for any user interfaces that are set.
func (s *UserInterfaceSpec) Default() {
	if s.PGAdmin != nil {
		s.PGAdmin.Default()
	}
}

// PostgresUserInterfaceStatus is a union of the supported PostgreSQL user
// interface statuses.
type PostgresUserInterfaceStatus struct {

	// The state of the pgAdmin user interface.
	PGAdmin PGAdminPodStatus `json:"pgAdmin,omitempty"`
}

type PostgresAdditionalConfig struct {
	// Settings that apply to the entire PgBouncer process.
	// More info: https://www.pgbouncer.org/config.html
	// +optional
	Global map[string]string `json:"global,omitempty"`
}

type RSAddrStatus struct {
	// +optional
	Host string `json:"host,omitempty"`
	// +optional
	Port int `json:"port,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +operator-sdk:csv:customresourcedefinitions:resources={{ConfigMap,v1},{Secret,v1},{Service,v1},{CronJob,v1beta1},{Deployment,v1},{Job,v1},{StatefulSet,v1},{PersistentVolumeClaim,v1}}

// PostgresCluster is the Schema for the postgresclusters API
type PostgresCluster struct {
	// ObjectMeta.Name is a DNS subdomain.
	// - https://docs.k8s.io/concepts/overview/working-with-objects/names/#dns-subdomain-names
	// - https://releases.k8s.io/v1.21.0/staging/src/k8s.io/apiextensions-apiserver/pkg/registry/customresource/validator.go#L60

	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// NOTE: Every PostgresCluster needs a Spec, but it is optional here
	// so ObjectMeta can be managed independently.

	Spec   PostgresClusterSpec   `json:"spec,omitempty"`
	Status PostgresClusterStatus `json:"status,omitempty"`
}

// Default implements "sigs.k8s.io/controller-runtime/pkg/webhook.Defaulter" so
// a webhook can be registered for the type.
// - https://book.kubebuilder.io/reference/webhook-overview.html
func (c *PostgresCluster) Default() {
	if len(c.APIVersion) == 0 {
		c.APIVersion = GroupVersion.String()
	}
	if len(c.Kind) == 0 {
		c.Kind = "PostgresCluster"
	}
	c.Spec.Default()
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// PostgresClusterList contains a list of PostgresCluster
type PostgresClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresCluster{}, &PostgresClusterList{})
}

func NewPostgresCluster() *PostgresCluster {
	cluster := &PostgresCluster{}
	cluster.SetGroupVersionKind(GroupVersion.WithKind("PostgresCluster"))
	return cluster
}
