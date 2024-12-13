/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"github.com/sqc157400661/kdb/apis/shared"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	PersistentVolumeResizing = "PersistentVolumeResizing"
	KDBInstanceProgressing   = "Progressing"
	ProxyAvailable           = "ProxyAvailable"
)

// KDBInstanceSpec defines the desired state of KDBInstance
type KDBInstanceSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// InstanceSet is the pod of KDB instance
	// +optional
	InstanceSet shared.InstanceSetSpec `json:"instance"`

	// +optional
	Leader HostInfo `json:"leader"`
	// The port on which kdb should listen.
	// +optional
	// +kubebuilder:default=5432
	// +kubebuilder:validation:Minimum=1024
	Port *int32 `json:"port,omitempty"`

	// DeployArch Deployment Architecture
	// +optional
	DeployArch string `json:"deployArch"`

	// Engine supports MySQL, PG, and so on
	// +optional
	Engine string `json:"engine"`

	// EngineVersion the major version of KDB engine installed in the image
	// +kubebuilder:validation:Required
	EngineVersion string `json:"engineVersion"`

	// EngineFullVersion the full version of KDB engine installed in the image
	// +optional
	EngineFullVersion string `json:"engineFullVersion"`

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

	Config map[string]string `json:"config,omitempty"`
}

// KDBInstanceStatus defines the observed state of KDBInstance
type KDBInstanceStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	InstanceSet shared.InstanceSetStatus `json:"instance,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`

	// PVCStatus
	// +optional
	PVCPhase corev1.PersistentVolumeClaimPhase `json:"pvcPhase,omitempty"`

	// conditions represent the observations of KDB pvc current state.
	// Known .status.conditions.type are: "PersistentVolumeResizing",
	// "Progressing", "ProxyAvailable"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +operator-sdk:csv:customresourcedefinitions:type=status,xDescriptors={"urn:alm:descriptor:io.kubernetes.conditions"}
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="age",type="date",JSONPath=".metadata.creationTimestamp"
// KDBInstance is the Schema for the KDBinstances API
type KDBInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KDBInstanceSpec   `json:"spec,omitempty"`
	Status KDBInstanceStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// KDBInstanceList contains a list of KDBInstance
type KDBInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KDBInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KDBInstance{}, &KDBInstanceList{})
}
