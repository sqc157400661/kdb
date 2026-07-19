package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	PostgreSQLDCSKubernetes = "kubernetes"
	PostgreSQLDCSEtcd       = "etcd"
)

// PostgreSQLSpec contains PostgreSQL engine specific settings.
type PostgreSQLSpec struct {
	// HA selects the PostgreSQL product HA profile and safety policy.
	// +optional
	HA *PostgreSQLHASpec `json:"ha,omitempty"`

	// PgBouncer configures an optional connection pool that consumes the
	// primary Service and never participates in leader election.
	// +optional
	PgBouncer *PostgreSQLPgBouncerSpec `json:"pgbouncer,omitempty"`
	// Patroni configures PostgreSQL high availability runtime.
	// +optional
	Patroni *PostgreSQLPatroniSpec `json:"patroni,omitempty"`

	// Backups configures PostgreSQL backup and archive settings.
	// +optional
	Backups *PostgreSQLBackupSpec `json:"backups,omitempty"`

	// CredentialSecretRef references a Secret that contains PostgreSQL bootstrap credentials.
	// When empty, the operator creates an instance-scoped Secret with generated passwords.
	// Expected keys: superuser-username, superuser-password, replication-username, replication-password.
	// +optional
	CredentialSecretRef *corev1.LocalObjectReference `json:"credentialSecretRef,omitempty"`

	// Exporter configures the optional PostgreSQL exporter sidecar.
	// +optional
	Exporter *PostgreSQLExporterSpec `json:"exporter,omitempty"`

	// Parameters contains PostgreSQL settings rendered into Patroni dynamic configuration.
	// +optional
	Parameters map[string]string `json:"parameters,omitempty"`

	// HBA appends custom pg_hba.conf rules after operator-managed mandatory rules.
	// +optional
	HBA []string `json:"hba,omitempty"`

	// Restore is an internal, one-shot NewInstance bootstrap contract populated
	// by the DBRestore controller. Console/OpenAPI users never write it directly.
	// +optional
	Restore *PostgreSQLRestoreBootstrapSpec `json:"restore,omitempty"`

	// Lifecycle contains platform-internal scaling, upgrade, storage rebuild
	// and extension policy requests. Users reach this contract through the
	// Console/OpenAPI lifecycle workflow rather than writing the CR directly.
	// +optional
	Lifecycle *PostgreSQLLifecycleSpec `json:"lifecycle,omitempty"`

	// DR configures an asynchronous cross-Kubernetes active-standby pair. The
	// local Kubernetes DCS remains authoritative for members; etcd3 stores only
	// cluster-level term, heartbeat, fencing and manual-promotion state.
	// +optional
	DR *PostgreSQLDRSpec `json:"dr,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!self.enabled || (self.manualPromotionOnly && size(self.clusterId) > 0 && size(self.peerClusterId) > 0 && self.clusterId != self.peerClusterId && size(self.scope) > 0 && size(self.etcd3.endpoints) > 0 && size(self.etcd3.secretRef.name) > 0)",message="enabled PostgreSQL DR requires distinct cluster IDs, scope, manual promotion and etcd3 endpoints/secret"
// +kubebuilder:validation:XValidation:rule="!self.enabled || self.role != 'standby' || size(self.walSource.primaryConnInfo) > 0 || size(self.walSource.archivePrefix) > 0",message="PostgreSQL DR standby requires a streaming or archive WAL source"
// +kubebuilder:validation:XValidation:rule="!oldSelf.enabled || (self.clusterId == oldSelf.clusterId && self.peerClusterId == oldSelf.peerClusterId && self.scope == oldSelf.scope)",message="enabled PostgreSQL DR cluster identity and scope are immutable"
type PostgreSQLDRSpec struct {
	Enabled bool `json:"enabled"`
	// +kubebuilder:validation:Enum=active;standby
	Role                string                `json:"role"`
	ClusterID           string                `json:"clusterId"`
	PeerClusterID       string                `json:"peerClusterId"`
	Scope               string                `json:"scope"`
	ManualPromotionOnly bool                  `json:"manualPromotionOnly"`
	Etcd3               PostgreSQLDREtcd3Spec `json:"etcd3"`
	WALSource           PostgreSQLDRWALSource `json:"walSource,omitempty"`
}

type PostgreSQLDREtcd3Spec struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=9
	Endpoints []string                    `json:"endpoints"`
	Prefix    string                      `json:"prefix,omitempty"`
	SecretRef corev1.LocalObjectReference `json:"secretRef"`
}

type PostgreSQLDRWALSource struct {
	PrimaryConnInfo string `json:"primaryConnInfo,omitempty"`
	ArchivePrefix   string `json:"archivePrefix,omitempty"`
}

type PostgreSQLLifecycleSpec struct {
	// Upgrade requests a minor rolling or irreversible adjacent-major upgrade.
	// +optional
	Upgrade *PostgreSQLUpgradeSpec `json:"upgrade,omitempty"`
	// Rebuild requests replacement of a lost replica on a newly provisioned PVC.
	// +optional
	Rebuild *PostgreSQLRebuildSpec `json:"rebuild,omitempty"`
	// Extensions is the desired reviewed extension catalog for the instance.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	Extensions []PostgreSQLExtensionSpec `json:"extensions,omitempty"`
}

type PostgreSQLUpgradeSpec struct {
	OperationID string `json:"operationId"`
	// +kubebuilder:validation:Enum=Minor;Major
	Type                 string `json:"type"`
	TargetMajorVersion   string `json:"targetMajorVersion,omitempty"`
	TargetFullVersion    string `json:"targetFullVersion,omitempty"`
	TargetImage          string `json:"targetImage"`
	MaintenanceWindow    string `json:"maintenanceWindow,omitempty"`
	RecoverableBackupRef string `json:"recoverableBackupRef,omitempty"`
	ExtensionsCompatible bool   `json:"extensionsCompatible,omitempty"`
	AcceptIrreversible   bool   `json:"acceptIrreversible,omitempty"`
}

type PostgreSQLRebuildSpec struct {
	OperationID string `json:"operationId"`
	PodName     string `json:"podName"`
	// +kubebuilder:validation:Enum=Primary;Backup
	Source           string `json:"source"`
	BackupRef        string `json:"backupRef,omitempty"`
	AbandonOldVolume bool   `json:"abandonOldVolume"`
}

type PostgreSQLExtensionSpec struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	// +optional
	CustomImage *PostgreSQLCustomExtensionImage `json:"customImage,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.image.matches('^.+@sha256:[0-9a-f]{64}$') && self.signatureVerified && self.scanStatus == 'Passed' && size(self.attestationRef) > 0",message="custom PostgreSQL extension images require digest, verified signature, passed scan and attestation"
type PostgreSQLCustomExtensionImage struct {
	// Image must be immutable and addressed by sha256 digest.
	// +kubebuilder:validation:MaxLength=512
	Image             string `json:"image"`
	SignatureVerified bool   `json:"signatureVerified"`
	// +kubebuilder:validation:Enum=Passed;Failed;Pending
	ScanStatus string `json:"scanStatus"`
	// +kubebuilder:validation:MaxLength=512
	AttestationRef string `json:"attestationRef,omitempty"`
}

// PostgreSQLHASpec maps the stable product profiles to kdb-ha settings.
type PostgreSQLHASpec struct {
	// Profile is single, standard-ha or strong-ha.
	// +optional
	// +kubebuilder:validation:Enum=single;standard-ha;strong-ha
	// +kubebuilder:default=single
	Profile string `json:"profile,omitempty"`

	// MaximumLagOnFailoverBytes rejects lagging automatic failover candidates.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaximumLagOnFailoverBytes *int64 `json:"maximumLagOnFailoverBytes,omitempty"`

	// MaximumLagOnSyncNodeBytes excludes lagging synchronous candidates.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaximumLagOnSyncNodeBytes *int64 `json:"maximumLagOnSyncNodeBytes,omitempty"`

	// SynchronousMode enables synchronous replication selection.
	// +optional
	SynchronousMode bool `json:"synchronousMode,omitempty"`

	// SynchronousModeStrict blocks commits instead of silently degrading when
	// the configured synchronous quorum is absent.
	// +optional
	SynchronousModeStrict bool `json:"synchronousModeStrict,omitempty"`

	// SynchronousNodeCount is the required acknowledgement quorum.
	// +optional
	// +kubebuilder:validation:Minimum=1
	SynchronousNodeCount *int32 `json:"synchronousNodeCount,omitempty"`
}

// PostgreSQLPgBouncerSpec declares the optional pooler deployment.
type PostgreSQLPgBouncerSpec struct {
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// +optional
	Image string `json:"image,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`
	// +optional
	// +kubebuilder:default=6432
	Port int32 `json:"port,omitempty"`
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// PostgreSQLBackupSpec contains backup-related PostgreSQL settings.
type PostgreSQLBackupSpec struct {
	// PGBackRest configures pgBackRest archive and backup behavior.
	// +optional
	PGBackRest *PostgreSQLPGBackRestSpec `json:"pgbackrest,omitempty"`
}

// PostgreSQLPGBackRestSpec contains pgBackRest settings.
// +kubebuilder:validation:XValidation:rule="!self.enabled || self.repoType != 's3' || (has(self.s3Bucket) && size(self.s3Bucket) > 0 && has(self.s3Endpoint) && size(self.s3Endpoint) > 0 && has(self.repoSecretRef) && size(self.repoSecretRef.name) > 0)",message="enabled S3 pgBackRest requires bucket, endpoint and repoSecretRef"
type PostgreSQLPGBackRestSpec struct {
	// Enabled controls whether WAL archiving through pgBackRest is enabled.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Stanza is the pgBackRest stanza name.
	// +optional
	// +kubebuilder:default=db
	Stanza string `json:"stanza,omitempty"`

	// RepoType selects the pgBackRest repository type.
	// +optional
	// +kubebuilder:validation:Enum=local;s3
	// +kubebuilder:default=local
	RepoType string `json:"repoType,omitempty"`

	// RepoPath is the local repository path when repoType=local.
	// +optional
	// +kubebuilder:default=/backrestrepo
	RepoPath string `json:"repoPath,omitempty"`

	// RepoSecretRef references credentials for non-local repositories.
	// +optional
	RepoSecretRef *corev1.LocalObjectReference `json:"repoSecretRef,omitempty"`

	// RetentionFull is pgBackRest repo1-retention-full.
	// +optional
	// +kubebuilder:validation:Minimum=1
	RetentionFull *int32 `json:"retentionFull,omitempty"`

	// RetentionDays defines the advertised continuous PITR window.
	// +optional
	// +kubebuilder:default=14
	// +kubebuilder:validation:Minimum=1
	RetentionDays *int32 `json:"retentionDays,omitempty"`

	// FullSchedule defaults to a weekly full backup.
	// +optional
	// +kubebuilder:default="0 1 * * 0"
	FullSchedule string `json:"fullSchedule,omitempty"`

	// DifferentialSchedule defaults to a daily differential backup.
	// +optional
	// +kubebuilder:default="0 1 * * 1-6"
	DifferentialSchedule string `json:"differentialSchedule,omitempty"`

	// ValidationSchedule controls isolated restore verification cadence.
	// +optional
	// +kubebuilder:default="0 3 * * 0"
	ValidationSchedule string `json:"validationSchedule,omitempty"`

	// S3Bucket, S3Endpoint and S3Region are required when repoType=s3.
	// +optional
	S3Bucket string `json:"s3Bucket,omitempty"`
	// +optional
	S3Endpoint string `json:"s3Endpoint,omitempty"`
	// +optional
	S3Region string `json:"s3Region,omitempty"`
	// +optional
	// +kubebuilder:default=path
	// +kubebuilder:validation:Enum=host;path
	S3URIStyle string `json:"s3UriStyle,omitempty"`
	// S3TLSVerify defaults to true. False exists for isolated development Gates.
	// +optional
	S3TLSVerify *bool `json:"s3TlsVerify,omitempty"`
}

type PostgreSQLRestoreBootstrapSpec struct {
	OperationID  string `json:"operationId"`
	BackupID     string `json:"backupId,omitempty"`
	TargetType   string `json:"targetType,omitempty"`
	Target       string `json:"target,omitempty"`
	TargetAction string `json:"targetAction,omitempty"`
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
	// Term is the validated, unexpired leader Lease term.
	// +optional
	Term int64 `json:"term,omitempty"`

	// Members contains term-validated runtime members.
	// +optional
	Members []PostgreSQLMemberStatus `json:"members,omitempty"`
	// Primary is the current primary Pod name when known.
	// +optional
	Primary string `json:"primary,omitempty"`

	// Replicas contains known replica Pod names.
	// +optional
	Replicas []string `json:"replicas,omitempty"`

	// Ready indicates whether at least one primary endpoint is available and all expected pods are ready.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// DynamicConfigRevision is the observed kdb-ha DCS configuration revision.
	// +optional
	DynamicConfigRevision int64 `json:"dynamicConfigRevision,omitempty"`

	// EffectiveConfigHash identifies the observed dynamic config without storing its values in status.
	// +optional
	EffectiveConfigHash string `json:"effectiveConfigHash,omitempty"`

	// BootstrapParameters records the one-time spec.postgresql.parameters merge.
	// +optional
	BootstrapParameters *PostgreSQLBootstrapParametersStatus `json:"bootstrapParameters,omitempty"`

	// Endpoints contains ready PostgreSQL pod network endpoints.
	// +optional
	Endpoints []HostInfo `json:"endpoints,omitempty"`

	// PGBackRest contains observed pgBackRest configuration state.
	// +optional
	PGBackRest *PostgreSQLPGBackRestStatus `json:"pgbackrest,omitempty"`

	// Lifecycle reports the current scale/upgrade/storage/extension workflow.
	// +optional
	Lifecycle *PostgreSQLLifecycleStatus `json:"lifecycle,omitempty"`

	// Services contains stable connection Service names.
	// +optional
	Services PostgreSQLServiceStatus `json:"services,omitempty"`

	// PgBouncer contains the optional pooler projection.
	// +optional
	PgBouncer *PostgreSQLPgBouncerStatus `json:"pgbouncer,omitempty"`

	// DR is projected from the kdb-ha etcd3 arbitration state.
	// +optional
	DR *PostgreSQLDRStatus `json:"dr,omitempty"`
}

type PostgreSQLDRStatus struct {
	Enabled             bool                             `json:"enabled,omitempty"`
	ClusterID           string                           `json:"clusterId,omitempty"`
	PeerClusterID       string                           `json:"peerClusterId,omitempty"`
	ConfiguredRole      string                           `json:"configuredRole,omitempty"`
	RuntimeRole         string                           `json:"runtimeRole,omitempty"`
	ActiveClusterID     string                           `json:"activeClusterId,omitempty"`
	Term                int64                            `json:"term,omitempty"`
	FencedClusterID     string                           `json:"fencedClusterId,omitempty"`
	FencedTerm          int64                            `json:"fencedTerm,omitempty"`
	Connected           bool                             `json:"connected,omitempty"`
	ManualPromotionOnly bool                             `json:"manualPromotionOnly,omitempty"`
	LastOperationID     string                           `json:"lastOperationId,omitempty"`
	RPOBytes            int64                            `json:"rpoBytes,omitempty"`
	RPOSeconds          int64                            `json:"rpoSeconds,omitempty"`
	LastPromotedAt      *metav1.Time                     `json:"lastPromotedAt,omitempty"`
	LastFencedAt        *metav1.Time                     `json:"lastFencedAt,omitempty"`
	LastDrill           *PostgreSQLDRDrillStatus         `json:"lastDrill,omitempty"`
	ClusterHeartbeats   map[string]PostgreSQLDRHeartbeat `json:"clusterHeartbeats,omitempty"`
}

type PostgreSQLDRHeartbeat struct {
	ClusterID  string      `json:"clusterId,omitempty"`
	Role       string      `json:"role,omitempty"`
	LSN        string      `json:"lsn,omitempty"`
	ObservedAt metav1.Time `json:"observedAt,omitempty"`
}

type PostgreSQLDRDrillStatus struct {
	OperationID  string       `json:"operationId,omitempty"`
	ApprovalRef  string       `json:"approvalRef,omitempty"`
	Approvers    []string     `json:"approvers,omitempty"`
	PreviousTerm int64        `json:"previousTerm,omitempty"`
	CurrentTerm  int64        `json:"currentTerm,omitempty"`
	RPOBytes     int64        `json:"rpoBytes,omitempty"`
	RPOSeconds   int64        `json:"rpoSeconds,omitempty"`
	RTOSeconds   int64        `json:"rtoSeconds,omitempty"`
	StartedAt    *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt  *metav1.Time `json:"completedAt,omitempty"`
}

type PostgreSQLLifecycleStatus struct {
	OperationID    string       `json:"operationId,omitempty"`
	Kind           string       `json:"kind,omitempty"`
	Phase          string       `json:"phase,omitempty"`
	CurrentVersion string       `json:"currentVersion,omitempty"`
	TargetVersion  string       `json:"targetVersion,omitempty"`
	PrimaryBefore  string       `json:"primaryBefore,omitempty"`
	PrimaryAfter   string       `json:"primaryAfter,omitempty"`
	JobName        string       `json:"jobName,omitempty"`
	MemberName     string       `json:"memberName,omitempty"`
	OldPVCUID      string       `json:"oldPvcUID,omitempty"`
	NewPVCUID      string       `json:"newPvcUID,omitempty"`
	Message        string       `json:"message,omitempty"`
	Irreversible   bool         `json:"irreversible,omitempty"`
	StartedAt      *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt    *metav1.Time `json:"completedAt,omitempty"`
}

type PostgreSQLBootstrapParametersStatus struct {
	InputHash string      `json:"inputHash,omitempty"`
	Revision  int64       `json:"revision,omitempty"`
	AppliedAt metav1.Time `json:"appliedAt,omitempty"`
	Method    string      `json:"method,omitempty"`
	State     string      `json:"state,omitempty"`
}

type PostgreSQLMemberStatus struct {
	Name        string      `json:"name"`
	PodUID      string      `json:"podUID,omitempty"`
	Role        string      `json:"role,omitempty"`
	Running     bool        `json:"running,omitempty"`
	Ready       bool        `json:"ready,omitempty"`
	Synchronous bool        `json:"synchronous,omitempty"`
	Timeline    int         `json:"timeline,omitempty"`
	LSN         string      `json:"lsn,omitempty"`
	Term        int64       `json:"term,omitempty"`
	NodeName    string      `json:"nodeName,omitempty"`
	Zone        string      `json:"zone,omitempty"`
	ObservedAt  metav1.Time `json:"observedAt,omitempty"`
}

type PostgreSQLServiceStatus struct {
	Headless  string `json:"headless,omitempty"`
	Primary   string `json:"primary,omitempty"`
	Replicas  string `json:"replicas,omitempty"`
	Any       string `json:"any,omitempty"`
	PgBouncer string `json:"pgbouncer,omitempty"`
}

type PostgreSQLPgBouncerStatus struct {
	Enabled       bool   `json:"enabled,omitempty"`
	Replicas      int32  `json:"replicas,omitempty"`
	ReadyReplicas int32  `json:"readyReplicas,omitempty"`
	ServiceName   string `json:"serviceName,omitempty"`
	Port          int32  `json:"port,omitempty"`
}

// PostgreSQLPGBackRestStatus contains observed pgBackRest state.
type PostgreSQLPGBackRestStatus struct {
	// Enabled reports whether pgBackRest archive is configured.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Stanza is the configured pgBackRest stanza.
	// +optional
	Stanza string `json:"stanza,omitempty"`

	// RepoType is the configured repository type.
	// +optional
	RepoType string `json:"repoType,omitempty"`

	// ConfigMapName is the ConfigMap containing pgbackrest.conf.
	// +optional
	ConfigMapName               string       `json:"configMapName,omitempty"`
	RetentionDays               int32        `json:"retentionDays,omitempty"`
	FullSchedule                string       `json:"fullSchedule,omitempty"`
	DifferentialSchedule        string       `json:"differentialSchedule,omitempty"`
	WALArchiveEnabled           bool         `json:"walArchiveEnabled,omitempty"`
	LatestBackupRef             string       `json:"latestBackupRef,omitempty"`
	LastSuccessfulBackupTime    *metav1.Time `json:"lastSuccessfulBackupTime,omitempty"`
	LastRestoreValidationTime   *metav1.Time `json:"lastRestoreValidationTime,omitempty"`
	LastRestoreValidationStatus string       `json:"lastRestoreValidationStatus,omitempty"`
	PITRWindowStart             *metav1.Time `json:"pitrWindowStart,omitempty"`
	PITRWindowEnd               *metav1.Time `json:"pitrWindowEnd,omitempty"`
}
