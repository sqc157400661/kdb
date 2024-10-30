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

// +kubebuilder:validation:Enum=Running;Pending;Failed
type ProxyStatus string

const (
	ProxyStatusRunning ProxyStatus = "Running"
	ProxyStatusPending ProxyStatus = "Pending"
	ProxyStatusFailed  ProxyStatus = "Failed"

	MysqlLabelKeyProxyName = "mysql.kdb.com/proxy-name"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MySQLProxySpec defines the desired state of MySQLProxy
type MySQLProxySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Pod is the pod of mysql instance
	// TODO: 这里需要和kube-mysql里保持同步
	Pod InstancePodSpec `json:"pod"`

	// Settings that servers to the entire proxy process.
	// +optional
	Servers []MySQLServer `json:"servers,omitempty"`

	// Service list
	// +optional
	Services []ServiceSpec `json:"services,omitempty"`
}

type MySQLServer struct {
	// +optional
	InstanceName string `json:"instanceName,omitempty"`

	// +optional
	SigmaCluster string `json:"sigmaCluster,omitempty"`
}

// MySQLProxyStatus defines the observed state of MySQLProxy
type MySQLProxyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// +optional
	Status ProxyStatus `json:"status,omitempty"`

	// PodInfo contains information about the working pod of mysql instance
	// +optional
	PodInfo PodStatusInfo `json:"podInfo,omitempty"`

	// ServiceStatus
	// +optional
	ServiceStatus []ServiceStatus `json:"serviceStatus,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// MySQLProxy is the Schema for the mysqlproxys API
type MySQLProxy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MySQLProxySpec   `json:"spec,omitempty"`
	Status MySQLProxyStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// MySQLProxyList contains a list of MySQLProxy
type MySQLProxyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MySQLProxy `json:"items"`
}

type ProxyPodSpec struct {
	InstancePodSpec
}

func init() {
	SchemeBuilder.Register(&MySQLProxy{}, &MySQLProxyList{})
}
