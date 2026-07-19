package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	DBBackupPhasePending   = "Pending"
	DBBackupPhaseRunning   = "Running"
	DBBackupPhaseSucceeded = "Succeeded"
	DBBackupPhaseFailed    = "Failed"
)

type DBBackupSpec struct {
	// +kubebuilder:validation:MinLength=1
	OperationID string                      `json:"operationId"`
	InstanceRef corev1.LocalObjectReference `json:"instanceRef"`
	// +kubebuilder:validation:Enum=full;diff;incr
	Type     string `json:"type"`
	Validate bool   `json:"validate,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type DBBackupArtifactStatus struct {
	BackupID      string       `json:"backupId,omitempty"`
	URI           string       `json:"uri,omitempty"`
	Type          string       `json:"type,omitempty"`
	Stanza        string       `json:"stanza,omitempty"`
	StartTime     *metav1.Time `json:"startTime,omitempty"`
	StopTime      *metav1.Time `json:"stopTime,omitempty"`
	StartLSN      string       `json:"startLsn,omitempty"`
	StopLSN       string       `json:"stopLsn,omitempty"`
	SizeBytes     int64        `json:"sizeBytes,omitempty"`
	RepoSizeBytes int64        `json:"repoSizeBytes,omitempty"`
}

type DBBackupStatus struct {
	Phase              string                  `json:"phase,omitempty"`
	ObservedGeneration int64                   `json:"observedGeneration,omitempty"`
	ExecutorPod        string                  `json:"executorPod,omitempty"`
	Artifact           *DBBackupArtifactStatus `json:"artifact,omitempty"`
	ValidationStatus   string                  `json:"validationStatus,omitempty"`
	WALStart           string                  `json:"walStart,omitempty"`
	WALEnd             string                  `json:"walEnd,omitempty"`
	PITRWindowStart    *metav1.Time            `json:"pitrWindowStart,omitempty"`
	PITRWindowEnd      *metav1.Time            `json:"pitrWindowEnd,omitempty"`
	Message            string                  `json:"message,omitempty"`
	StartedAt          *metav1.Time            `json:"startedAt,omitempty"`
	CompletedAt        *metav1.Time            `json:"completedAt,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Instance",type=string,JSONPath=.spec.instanceRef.name
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=.spec.type
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=.status.phase
type DBBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DBBackupSpec   `json:"spec,omitempty"`
	Status            DBBackupStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type DBBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DBBackup `json:"items"`
}

func init() { SchemeBuilder.Register(&DBBackup{}, &DBBackupList{}) }
