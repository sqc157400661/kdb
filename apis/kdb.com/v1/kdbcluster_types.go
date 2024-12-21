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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type HostInfo struct {
	Hostname string `json:"hostname"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

type InstanceDesc struct {
	Name string `json:"name,omitempty"`
	// Number of desired KDB db pods.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// RuntimeClassName refers to a RuntimeClass object in the node.k8s.io group, which should be used
	// to run this pod.  If no RuntimeClass resource matches the named class, the pod will not be run.
	// If unset or empty, the "legacy" RuntimeClass will be used, which is an implicit class with an
	// empty definition that uses the default runtime handler.
	// More info: https://git.k8s.io/enhancements/keps/sig-node/585-runtime-class
	// +optional
	RuntimeClassName *string `json:"runtimeClassName,omitempty"`

	// Priority class name for the KDB pod. Changing this value causes
	// KDB pod to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/pod-priority-preemption/
	// +optional
	PriorityClassName *string `json:"priorityClassName,omitempty"`

	// Scheduling constraints of a KDB pod. Changing this value causes
	// instance to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Tolerations of a KDB pod. Changing this value causes KDB pod to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// +optional
	StorageClass string `json:"storageClass"`

	Size resource.Quantity `json:"size"`

	LogSize resource.Quantity `json:"logSize"`

	// The port on which kdb should listen.
	// +optional
	// +kubebuilder:default=5432
	// +kubebuilder:validation:Minimum=1024
	Port *int32 `json:"port,omitempty"`

	// EngineFullVersion the full version of KDB engine installed in the image
	// +optional
	EngineFullVersion string `json:"engineFullVersion"`
}

// KDBClusterSpec defines the desired state of KDBCluster
type KDBClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Instances is the pod of KDB instances
	// +optional
	Instances []InstanceDesc `json:"instances,omitempty"`

	// +optional
	Leader HostInfo `json:"leader"`

	// DeployArch Deployment Architecture
	// +optional
	DeployArch string `json:"deployArch"`

	// Engine supports MySQL, PG, and so on
	// +optional
	Engine string `json:"engine"`

	// EngineVersion the major version of KDB engine installed in the image
	// +kubebuilder:validation:Required
	EngineVersion string `json:"engineVersion"`
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
