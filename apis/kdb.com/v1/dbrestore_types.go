package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	DBRestorePhasePending    = "Pending"
	DBRestorePhaseRestoring  = "Restoring"
	DBRestorePhaseValidating = "Validating"
	DBRestorePhaseSucceeded  = "Succeeded"
	DBRestorePhaseFailed     = "Failed"
)

type DBRestoreTarget struct {
	// +kubebuilder:validation:Enum=backup;time;lsn;restore-point
	Type  string `json:"type,omitempty"`
	Value string `json:"value,omitempty"`
}

type DBRestoreSpec struct {
	// +kubebuilder:validation:MinLength=1
	OperationID string                      `json:"operationId"`
	BackupRef   corev1.LocalObjectReference `json:"backupRef"`
	// Only NewInstance is accepted in phase 1. InPlace is intentionally absent.
	// +kubebuilder:validation:Enum=NewInstance
	Mode string `json:"mode"`
	// +kubebuilder:validation:MinLength=1
	TargetInstanceName string          `json:"targetInstanceName"`
	Target             DBRestoreTarget `json:"target,omitempty"`
	Reason             string          `json:"reason,omitempty"`
}

type DBRestoreStatus struct {
	Phase              string                       `json:"phase,omitempty"`
	ObservedGeneration int64                        `json:"observedGeneration,omitempty"`
	SourceInstanceRef  *corev1.LocalObjectReference `json:"sourceInstanceRef,omitempty"`
	TargetInstanceRef  *corev1.LocalObjectReference `json:"targetInstanceRef,omitempty"`
	RestoredBackupID   string                       `json:"restoredBackupId,omitempty"`
	Message            string                       `json:"message,omitempty"`
	StartedAt          *metav1.Time                 `json:"startedAt,omitempty"`
	CompletedAt        *metav1.Time                 `json:"completedAt,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Backup",type=string,JSONPath=.spec.backupRef.name
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=.spec.targetInstanceName
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=.status.phase
type DBRestore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DBRestoreSpec   `json:"spec,omitempty"`
	Status            DBRestoreStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type DBRestoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DBRestore `json:"items"`
}

func init() { SchemeBuilder.Register(&DBRestore{}, &DBRestoreList{}) }
