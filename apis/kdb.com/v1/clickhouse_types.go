package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/sqc157400661/kdb/apis/shared"
)

type ClickHouseComputeGroupRole string

const (
	ClickHouseRoleIngest  ClickHouseComputeGroupRole = "Ingest"
	ClickHouseRoleServing ClickHouseComputeGroupRole = "Serving"
	ClickHouseRoleAdhoc   ClickHouseComputeGroupRole = "Adhoc"
)

type ClickHouseKeeperMode string

const (
	ClickHouseKeeperDedicated ClickHouseKeeperMode = "Dedicated"
	ClickHouseKeeperSharedRef ClickHouseKeeperMode = "SharedRef"
)

type ClickHouseSpec struct {
	DataShards        int32                        `json:"dataShards"`
	StorageProfileRef string                       `json:"storageProfileRef,omitempty"`
	Keeper            ClickHouseKeeperSpec         `json:"keeper"`
	ComputeGroups     []ClickHouseComputeGroupSpec `json:"computeGroups"`
	Gateway           *ClickHouseGatewaySpec       `json:"gateway,omitempty"`
	Backup            *ClickHouseBackupSpec        `json:"backup,omitempty"`
}

type ClickHouseKeeperSpec struct {
	Mode     ClickHouseKeeperMode         `json:"mode"`
	Replicas *int32                       `json:"replicas,omitempty"`
	Ref      *corev1.LocalObjectReference `json:"ref,omitempty"`
	Instance *shared.InstanceSetSpec      `json:"instance,omitempty"`
}

type ClickHouseComputeGroupSpec struct {
	Name              string                               `json:"name"`
	Role              ClickHouseComputeGroupRole           `json:"role"`
	StorageProfileRef string                               `json:"storageProfileRef,omitempty"`
	Instance          shared.InstanceSetSpec               `json:"instance"`
	Lifecycle         *ClickHouseComputeGroupLifecycleSpec `json:"lifecycle,omitempty"`
}

type ClickHouseComputeGroupLifecycleSpec struct {
	AutoSuspendEnabled   *bool            `json:"autoSuspendEnabled,omitempty"`
	AutoSuspendAfter     *metav1.Duration `json:"autoSuspendAfter,omitempty"`
	WarmReplicasPerShard *int32           `json:"warmReplicasPerShard,omitempty"`
}

type ClickHouseGatewaySpec struct {
	Enabled          *bool                        `json:"enabled,omitempty"`
	Replicas         *int32                       `json:"replicas,omitempty"`
	Service          *shared.ServiceSpec          `json:"service,omitempty"`
	BindingSecretRef *corev1.LocalObjectReference `json:"bindingSecretRef,omitempty"`
}

type ClickHouseBackupSpec struct {
	Enabled          *bool                        `json:"enabled,omitempty"`
	ObjectStorageRef *corev1.LocalObjectReference `json:"objectStorageRef,omitempty"`
}

type ClickHouseStatus struct {
	DataShards    int32                          `json:"dataShards,omitempty"`
	Keeper        *ClickHouseKeeperStatus        `json:"keeper,omitempty"`
	Gateway       *ClickHouseGatewayStatus       `json:"gateway,omitempty"`
	ComputeGroups []ClickHouseComputeGroupStatus `json:"computeGroups,omitempty"`
	Version       *ClickHouseVersionStatus       `json:"version,omitempty"`
}

type ClickHouseKeeperStatus struct {
	Members      int32 `json:"members,omitempty"`
	ReadyMembers int32 `json:"readyMembers,omitempty"`
}

type ClickHouseGatewayStatus struct {
	ReadyReplicas  int32  `json:"readyReplicas,omitempty"`
	HTTPEndpoint   string `json:"httpEndpoint,omitempty"`
	NativeEndpoint string `json:"nativeEndpoint,omitempty"`
	NativePhase    string `json:"nativePhase,omitempty"`
}

type ClickHouseComputeGroupStatus struct {
	Name                       string                     `json:"name"`
	Role                       ClickHouseComputeGroupRole `json:"role"`
	Phase                      string                     `json:"phase"`
	ReplicasPerShard           int32                      `json:"replicasPerShard,omitempty"`
	ReadyReplicas              int32                      `json:"readyReplicas,omitempty"`
	UpdatedReplicas            int32                      `json:"updatedReplicas,omitempty"`
	MaxReplicationDelaySeconds int64                      `json:"maxReplicationDelaySeconds,omitempty"`
	ReadonlyReplicas           int32                      `json:"readonlyReplicas,omitempty"`
}

type ClickHouseVersionStatus struct {
	Min string `json:"min,omitempty"`
	Max string `json:"max,omitempty"`
}
