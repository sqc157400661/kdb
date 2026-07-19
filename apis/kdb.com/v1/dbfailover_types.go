package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	DBFailoverPhasePending             = "Pending"
	DBFailoverPhaseWaitingForApproval  = "WaitingForApproval"
	DBFailoverPhaseWaitingForCandidate = "WaitingForCandidate"
	DBFailoverPhaseRunning             = "Running"
	DBFailoverPhaseSucceeded           = "Succeeded"
	DBFailoverPhaseFailed              = "Failed"
)

// DBFailoverSpec is the internal durable contract for PostgreSQL HA actions.
// Console and external clients use the stable OpenAPI and never write this CRD directly.
type DBFailoverSpec struct {
	// OperationID is the globally idempotent operation key.
	// +kubebuilder:validation:MinLength=1
	OperationID string                      `json:"operationId"`
	InstanceRef corev1.LocalObjectReference `json:"instanceRef"`
	// +kubebuilder:validation:Enum=switchover;failover;rejoin;reinit;restart;pause;resume;dr-fence;dr-promote
	Mode      string `json:"mode"`
	Candidate string `json:"candidate,omitempty"`
	// ClusterID is required for cross-Kubernetes DR fencing and promotion.
	ClusterID string `json:"clusterId,omitempty"`
	// +kubebuilder:validation:Minimum=1
	ExpectedTerm int64 `json:"expectedTerm,omitempty"`
	Force        bool  `json:"force,omitempty"`
	// DataLossAcceptance must equal AcceptPotentialDataLoss for forced failover.
	DataLossAcceptance string   `json:"dataLossAcceptance,omitempty"`
	ApprovalRef        string   `json:"approvalRef,omitempty"`
	Approvers          []string `json:"approvers,omitempty"`
	// FencedTerm confirms external fencing of the former primary term.
	FencedTerm  int64  `json:"fencedTerm,omitempty"`
	RestartMode string `json:"restartMode,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type DBFailoverStepStatus struct {
	Name      string      `json:"name"`
	Phase     string      `json:"phase"`
	Message   string      `json:"message,omitempty"`
	UpdatedAt metav1.Time `json:"updatedAt"`
}

type DBFailoverStatus struct {
	Phase                  string                 `json:"phase,omitempty"`
	ObservedGeneration     int64                  `json:"observedGeneration,omitempty"`
	PreviousPrimary        string                 `json:"previousPrimary,omitempty"`
	CurrentPrimary         string                 `json:"currentPrimary,omitempty"`
	PreviousTerm           int64                  `json:"previousTerm,omitempty"`
	CurrentTerm            int64                  `json:"currentTerm,omitempty"`
	EstimatedDataLossBytes int64                  `json:"estimatedDataLossBytes,omitempty"`
	ObservedRPOSeconds     int64                  `json:"observedRpoSeconds,omitempty"`
	RTOSeconds             int64                  `json:"rtoSeconds,omitempty"`
	ApprovalRef            string                 `json:"approvalRef,omitempty"`
	Approvers              []string               `json:"approvers,omitempty"`
	EventID                string                 `json:"eventId,omitempty"`
	Message                string                 `json:"message,omitempty"`
	StartedAt              *metav1.Time           `json:"startedAt,omitempty"`
	CompletedAt            *metav1.Time           `json:"completedAt,omitempty"`
	Steps                  []DBFailoverStepStatus `json:"steps,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=.spec.mode
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=.status.phase
// +kubebuilder:printcolumn:name="Operation",type=string,JSONPath=.spec.operationId
type DBFailover struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DBFailoverSpec   `json:"spec,omitempty"`
	Status            DBFailoverStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type DBFailoverList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DBFailover `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DBFailover{}, &DBFailoverList{})
}
