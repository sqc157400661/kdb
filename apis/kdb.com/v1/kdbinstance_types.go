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
	"github.com/sqc157400661/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/sqc157400661/kdb/apis/shared"
)

const (
	PersistentVolumeResizing = "PersistentVolumeResizing"
	KDBInstanceProgressing   = "Progressing"
	ProxyAvailable           = "ProxyAvailable"
)

// KDBInstanceSpec defines the desired state of KDBInstance
// +kubebuilder:validation:XValidation:rule="self.deployArch != 'Master-Slave' || (has(self.instance) && has(self.instance.replicas) && self.instance.replicas == 2)",message="when deployArch is Master-Slave, spec.instance.replicas must be 2"
// +kubebuilder:validation:XValidation:rule="self.deployArch != 'Master-Replica' || (has(self.instance) && has(self.instance.replicas) && self.instance.replicas >= 2)",message="when deployArch is Master-Replica, spec.instance.replicas must be greater than or equal to 2"
// +kubebuilder:validation:XValidation:rule="self.deployArch != 'MGR' || (has(self.instance) && has(self.instance.replicas) && self.instance.replicas >= 3)",message="when deployArch is MGR, spec.instance.replicas must be greater than or equal to 3"
// +kubebuilder:validation:XValidation:rule="self.deployArch != 'MGR' || !has(self.mysql) || !has(self.mysql.mgr) || !has(self.mysql.mgr.bootstrapOrdinal) || self.mysql.mgr.bootstrapOrdinal < self.instance.replicas",message="when deployArch is MGR, spec.mysql.mgr.bootstrapOrdinal must be less than spec.instance.replicas"
// +kubebuilder:validation:XValidation:rule="!has(self.mysql) || !has(self.mysql.mgr) || !has(self.mysql.mgr.groupPort) || !has(self.port) || self.mysql.mgr.groupPort != self.port",message="spec.mysql.mgr.groupPort must not equal spec.port"
// +kubebuilder:validation:XValidation:rule="self.engine != 'postgresql' && self.engine != 'pg' || self.deployArch != 'MGR'",message="deployArch MGR is only supported by MySQL"
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
	// +kubebuilder:validation:Enum=Single;Master-Slave;Master-Replica;MGR
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

	// MySQL contains MySQL engine specific settings.
	// +optional
	MySQL *MySQLSpec `json:"mysql,omitempty"`

	// Proxy declares the optional instance-level database proxy layer.
	// For MySQL, the first supported implementation is ProxySQL.
	// +optional
	Proxy *KDBProxySpec `json:"proxy,omitempty"`

	// PostgreSQL contains PostgreSQL engine specific settings.
	// +optional
	PostgreSQL *PostgreSQLSpec `json:"postgresql,omitempty"`

	// Shutdown requests a logical stop of the KDB instance.
	// When true, StatefulSet pods are scaled to zero while PVCs and other
	// persistent resources remain in place for a later start.
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

	// PostgreSQL contains observed PostgreSQL runtime state.
	// +optional
	PostgreSQL *PostgreSQLStatus `json:"postgresql,omitempty"`

	// Proxy contains observed state of the instance-level proxy layer.
	// +optional
	Proxy *KDBProxyStatus `json:"proxy,omitempty"`

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

func (k *KDBInstance) Default() {
	if k == nil {
		return
	}
	if k.Spec.InstanceSet.Replicas == nil {
		k.Spec.InstanceSet.Replicas = util.Int32(1)
	}
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
