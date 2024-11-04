package v1beta1

import (
	"github.com/sqc157400661/kdb/apis/shared"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const DefaultPGPort = 5432

// PostgresInstanceSpec defines the desired state of PostgresInstance
type PostgresInstanceSpec struct {
	// PostgreSQL backup configuration
	// +optional
	Backups Backups `json:"backups"`

	// postgres instance
	InstanceSet shared.InstanceSetSpec `json:"instance"`

	// +optional
	Patroni *PatroniSpec `json:"patroni,omitempty"`

	// The port on which PostgreSQL should listen.
	// +optional
	// +kubebuilder:default=5432
	// +kubebuilder:validation:Minimum=1024
	Port *int32 `json:"port,omitempty"`

	// The major version of PostgreSQL installed in the PostgreSQL image
	// +kubebuilder:validation:Required
	PostgresVersion string `json:"postgresVersion"`

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

	// Specification of the service that exposes the PostgreSQL primary instance.
	// +optional
	Service *shared.ServiceSpec `json:"service,omitempty"`

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
	// PostgresInstance name. An empty list creates no users. Removing a user
	// from this list does NOT drop the user nor revoke their access.
	// +listType=map
	// +listMapKey=name
	// +optional
	Users []PostgresUserSpec `json:"users,omitempty"`

	Config map[string]string `json:"config,omitempty"`
}

// MonitoringStatus is the current state of PostgreSQL cluster monitoring tool
// configuration
type MonitoringStatus struct {
	// +optional
	ExporterConfiguration string `json:"exporterConfiguration,omitempty"`
}

// Default defines several key default values for a Postgres cluster.
func (s *PostgresInstanceSpec) Default() {
	s.InstanceSet.Default(1)

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

}

// Backups defines a PostgreSQL archive configuration
type Backups struct {

	// pgBackRest archive configuration
	// +optional
	PGBackRest PGBackRestArchive `json:"pgbackrest"`
}

// PostgresInstanceStatus defines the observed state of PostgresInstance
type PostgresInstanceStatus struct {
	// Current state of PostgreSQL instance.
	// +optional
	InstanceSet shared.InstanceSetStatus `json:"instance,omitempty"`

	// +optional
	Patroni PatroniStatus `json:"patroni,omitempty"`

	// Status information for pgBackRest
	// +optional
	PGBackRest *PGBackRestStatus `json:"pgbackrest,omitempty"`

	// Current state of the PostgreSQL proxy.
	// +optional
	Proxy PostgresProxyStatus `json:"proxy,omitempty"`

	// The instance that should be started first when bootstrapping and/or starting a
	// PostgresInstance.
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

	// conditions represent the observations of PostgresInstance's current state.
	// Known .status.conditions.type are: "PersistentVolumeResizing",
	// "Progressing", "ProxyAvailable"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +operator-sdk:csv:customresourcedefinitions:type=status,xDescriptors={"urn:alm:descriptor:io.kubernetes.conditions"}
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PostgresInstanceStatus condition types.
const (
	PersistentVolumeResizing    = "PersistentVolumeResizing"
	PostgresInstanceProgressing = "Progressing"
	ProxyAvailable              = "ProxyAvailable"
)

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

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +operator-sdk:csv:customresourcedefinitions:resources={{ConfigMap,v1},{Secret,v1},{Service,v1},{CronJob,v1beta1},{Deployment,v1},{Job,v1},{StatefulSet,v1},{PersistentVolumeClaim,v1}}

// PostgresInstance is the Schema for the PostgresInstances API
type PostgresInstance struct {
	// ObjectMeta.Name is a DNS subdomain.
	// - https://docs.k8s.io/concepts/overview/working-with-objects/names/#dns-subdomain-names
	// - https://releases.k8s.io/v1.21.0/staging/src/k8s.io/apiextensions-apiserver/pkg/registry/customresource/validator.go#L60

	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// NOTE: Every PostgresInstance needs a Spec, but it is optional here
	// so ObjectMeta can be managed independently.

	Spec   PostgresInstanceSpec   `json:"spec,omitempty"`
	Status PostgresInstanceStatus `json:"status,omitempty"`
}

// Default implements "sigs.k8s.io/controller-runtime/pkg/webhook.Defaulter" so
// a webhook can be registered for the type.
// - https://book.kubebuilder.io/reference/webhook-overview.html
func (c *PostgresInstance) Default() {
	if len(c.APIVersion) == 0 {
		c.APIVersion = GroupVersion.String()
	}
	if len(c.Kind) == 0 {
		c.Kind = "PostgresInstance"
	}
	c.Spec.Default()
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// PostgresInstanceList contains a list of PostgresInstance
type PostgresInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresInstance{}, &PostgresInstanceList{})
}

func NewPostgresInstance() *PostgresInstance {
	cluster := &PostgresInstance{}
	cluster.SetGroupVersionKind(GroupVersion.WithKind("PostgresInstance"))
	return cluster
}
