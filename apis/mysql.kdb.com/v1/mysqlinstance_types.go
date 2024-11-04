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
	FinalizerMySQLInstanceSubresources = "mysql.kdb.com/mysql-subresources"
)

// +kubebuilder:validation:Enum=InternalPVL;ExternalXvip
type ServiceType string

// MySQLInstanceSpec defines the desired state of MySQLInstance
type MySQLInstanceSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// InstanceSet is the pod of mysql instance
	InstanceSet shared.InstanceSetSpec `json:"instance"`

	// Whether or not the PostgreSQL cluster should be stopped.
	// When this is true, workloads are scaled to zero and CronJobs
	// are suspended.
	// Other resources, such as Services and Volumes, remain in place.
	// +optional
	Shutdown *bool `json:"shutdown,omitempty"`

	Config map[string]string `json:"config,omitempty"`
}

// MySQLInstanceStatus defines the observed state of MySQLInstance
type MySQLInstanceStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	InstanceSet shared.InstanceSetStatus `json:"instance,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`

	// PVCStatus
	// +optional
	PVCPhase corev1.PersistentVolumeClaimPhase `json:"pvcPhase,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="podIP",type=string,JSONPath=`.status.podInfo.podIP`
// +kubebuilder:printcolumn:name="node",type=string,JSONPath=`.status.podInfo.nodeName`
// +kubebuilder:printcolumn:name="hostIP",type=string,JSONPath=`.status.podInfo.hostIP`
// +kubebuilder:printcolumn:name="podPhase",type=string,JSONPath=`.status.podInfo.podPhase`
// +kubebuilder:printcolumn:name="pvcPhase",type=string,JSONPath=`.status.pvcPhase`
// +kubebuilder:printcolumn:name="status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="age",type="date",JSONPath=".metadata.creationTimestamp"
// MySQLInstance is the Schema for the mysqlinstances API
type MySQLInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec MySQLInstanceSpec `json:"spec,omitempty"`

	// +optional
	Status MySQLInstanceStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// MySQLInstanceList contains a list of MySQLInstance
type MySQLInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MySQLInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MySQLInstance{}, &MySQLInstanceList{})
}
