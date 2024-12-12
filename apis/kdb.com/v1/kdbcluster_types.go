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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LeaderInfo struct {
	Hostname string `json:"hostname"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

// KDBClusterSpec defines the desired state of KDBCluster
type KDBClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Instances is the pod of KDB instances
	// +optional
	Instances []KDBInstanceSpec `json:"instances,omitempty"`

	// +optional
	Leader string `json:"leader"`

	// DeployArch Deployment Architecture
	// +optional
	DeployArch string `json:"deployArch"`
}

// KDBClusterStatus defines the observed state of KDBCluster
type KDBClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Total number of  instance.
	// +optional
	TotalNum int32 `json:"totalNum,omitempty"`

	// Total number of ready instance.
	// +optional
	ReadyNum int32 `json:"readyNum,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`

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
// KDBCluster is the Schema for the KDBClusters API
type KDBCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KDBClusterSpec   `json:"spec,omitempty"`
	Status KDBClusterStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// KDBInstanceList contains a list of KDBCluster
type KDBClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KDBInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KDBCluster{}, &KDBClusterList{})
}
