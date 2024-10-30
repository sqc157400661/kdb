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

const (
	FinalizerMySQLInstanceSubresources = "mysql.kdb.com/mysql-subresources"
	// for XVIP service and PVL services
	FinalizerServiceLbs = "mysql.kdb.com/svc-lbs"

	// service annotation key
	SvcAnnoKeyXvipTemplateName = "mysql.kdb.com/xvip-template-config"
	// "true" allow creating public xvip
	SvcAnnoKeyCreatePublicXvip = "mysql.kdb.com/xvip-create-public"
	// xvip or pvl
	SvcAnnoKeyProvisioner = "service.k8s.alipay.com/provisioner"
	// if value is true, the service is controller by PVLServiceController
	SvcAnnoKeyForcePvlControl = "mysql.kdb.com/force-pvl-control"

	// value is xvip ip
	SvcAnnoKeyXvipPrecreation = "service.k8s.alipay.com/xvip-precreation"
	// xvip creation id
	SvcAnnoKeyXvipCreationOrderId = "mysql.kdb.com/xvip-creation-id"
	// pvl lb id
	SvcAnnoKeyPvlLbId = "mysql.kdb.com/pvl-lb-id"
	// pvl service id
	SvcAnnoKeyPvlServiceId = "mysql.kdb.com/pvl-svc-id"
	// pvl endpoint id
	SvcAnnoKeyPvlEndpointId = "mysql.kdb.com/pvl-ep-id"
	// pvl endpoint ip
	SvcAnnoKeyPvlEndpointEniIP = "mysql.kdb.com/pvl-ep-ip"

	// user pvc id
	SvcAnnoKeyPvlEndpointVpcId = "mysql.kdb.com/pvl-ep-vpc-id"
	// user zones
	SvcAnnoKeyPvlEndpointZones = "mysql.kdb.com/pvl-ep-zones"
	// lb spec
	SvcAnnoKeyLbSpec = "mysql.kdb.com/lb-spec"
	// force to clear all LB rs
	SvcAnnoKeyForceEmptyRS = "mysql.kdb.com/force-empty-rs"

	SvcTypePvlIntranet  = "PvlIntranet"
	SvcTypeXvipInternet = "XvipInternet"

	ServiceProvisionerXvip = "xvip"
	ServiceProvisionerPvl  = "pvl"

	ServiceTypeInternalPVL  ServiceType = "InternalPVL"
	ServiceTypeExternalXvip ServiceType = "ExternalXvip"

	InstanceStatusRunning InstanceStatus = "Running"
	InstanceStatusPending InstanceStatus = "Pending"
	InstanceStatusFailed  InstanceStatus = "Failed"
)

const (
	MysqlLabelKeyInstanceName = "mysql.kdb.com/instance-name"

	MysqlLableKeyWorkingPodName = "mysql.kdb.com/working-pod-name"

	MysqlAnnoKeyReportInterval = "mysql.kdb.com/sidecar-report-interval"

	// remove all pvl rs
	MysqlAnnoKeyDisablePvlRS = "mysql.kdb.com/disable-pvl-rs"

	// restore from backup
	// mysql cluster to recover
	MysqlAnnoKeyRestoreClusterId = "mysql.kdb.com/restore-clusterid"
	// the point-in-time to recover
	MysqlAnnoKeyRestorePointInTime = "mysql.kdb.com/restore-unix-pit"

	// full backup cron expression
	MysqlAnnoKeyFullBackupCron = "mysql.kdb.com/fullbackup-cron"
	// incr backup cron expression
	MysqlAnnoKeyIncrBackupCron = "mysql.kdb.com/incrbackup-cron"
	// MySQL serverId config
	MySQLAnnoKeyServerId = "mysql.kdb.com/server-id"
	// MySQL master instance name
	MySQLAnnoKeyMasterInstanceName = "mysql.kdb.com/master-instance"
	// MySQL master sigma cluster
	MySQLAnnoKeyMasterSigmaCluster = "mysql.kdb.com/master-sigma-cluster"
)

// +kubebuilder:validation:Enum=InternalPVL;ExternalXvip
type ServiceType string

// +kubebuilder:validation:Enum=Running;Pending;Failed
type InstanceStatus string

// MySQLInstanceSpec defines the desired state of MySQLInstance
type MySQLInstanceSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Pod is the pod of mysql instance
	Pod InstancePodSpec `json:"pod"`

	// PVC is the pvc of mysql instance
	PVC PVCSpec `json:"pvc"`

	// Service list
	// +optional
	Services []ServiceSpec `json:"services,omitempty"`
}

// MySQLInstanceStatus defines the observed state of MySQLInstance
type MySQLInstanceStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	Status InstanceStatus `json:"status,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`

	// PodInfo contains information about the working pod of mysql instance
	// +optional
	PodInfo PodStatusInfo `json:"podInfo,omitempty"`

	// PVCStatus
	// +optional
	PVCPhase corev1.PersistentVolumeClaimPhase `json:"pvcPhase,omitempty"`

	// ServiceStatus
	// +optional
	ServiceStatus []ServiceStatus `json:"serviceStatus,omitempty"`
}

type PodStatusInfo struct {
	// +optional
	PodName string `json:"podName,omitempty"`

	// PodStatus
	// +optional
	PodPhase corev1.PodPhase `json:"podPhase,omitempty"`

	// +optional
	PodIP string `json:"podIP,omitempty"`

	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// +optional
	HostIP string `json:"hostIP,omitempty"`
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

type InstancePodSpec struct {
	// pod annotations
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// pod labels
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// runtimeclass name
	// +optional
	RuntimeClass string `json:"runtimeClass,omitempty"`

	// pod afinnity
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// pod tolerations
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// pod initcontainer
	// +optional
	InitContainer ContainerSpec `json:"initContainer"`

	// pod mysqld container
	// +optional
	MainContainer ContainerSpec `json:"mainContainer"`

	// pod sidecar container
	// +optional
	SidecarContainer ContainerSpec `json:"sidecarContainer"`
}

type ContainerSpec struct {
	// +optional
	Image string `json:"image"`

	// +optional
	Command []string `json:"command,omitempty"`

	// +optional
	Args []string `json:"args,omitempty"`

	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

type PVCSpec struct {
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// +optional
	StorageClass string `json:"storageClass"`

	Size resource.Quantity `json:"size"`
}

type ServiceSpec struct {
	Name string `json:"name"`

	Type ServiceType `json:"type"`

	// DomainZone is the suffix of domain name
	// +optional
	DomainZone string `json:"domainZone,omitempty"`
}

type ServiceStatus struct {
	Name string `json:"name"`
	// +optional
	LoadBalancer corev1.LoadBalancerStatus `json:"loadBalancer,omitempty"`
}

func init() {
	SchemeBuilder.Register(&MySQLInstance{}, &MySQLInstanceList{})
}
