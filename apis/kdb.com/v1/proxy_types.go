package v1

import (
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

const (
	ProxyTypeProxySQL = "ProxySQL"

	ProxyConfigSourceInline       = "Inline"
	ProxyConfigSourceConfigMapRef = "ConfigMapRef"

	ProxyBackendDiscoveryOperator = "Operator"

	ProxyLoadBalanceRoundRobin = "RoundRobin"

	ProxyConditionAvailable = "ProxyAvailable"
)

// KDBProxySpec declares the cluster level proxy layer.
type KDBProxySpec struct {
	// +optional
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=ProxySQL
	// +kubebuilder:default=ProxySQL
	Type string `json:"type,omitempty"`

	// +optional
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// +optional
	Image string `json:"image,omitempty"`

	// +optional
	MgrImage string `json:"mgrImage,omitempty"`

	// +optional
	Config *KDBProxyConfigSpec `json:"config,omitempty"`

	// +optional
	Service *KDBProxyServiceSpec `json:"service,omitempty"`

	// +optional
	Resources *KDBProxyResourceSpec `json:"resources,omitempty"`
}

type KDBProxyConfigSpec struct {
	// +optional
	// +kubebuilder:validation:Enum=Inline;ConfigMapRef
	// +kubebuilder:default=Inline
	Source string `json:"source,omitempty"`

	// +optional
	ConfigMapRef *KDBProxyConfigMapRef `json:"configMapRef,omitempty"`

	// +optional
	Inline *KDBProxyInlineConfigSpec `json:"inline,omitempty"`
}

type KDBProxyConfigMapRef struct {
	// +optional
	Name string `json:"name,omitempty"`

	// +optional
	Key string `json:"key,omitempty"`
}

type KDBProxyInlineConfigSpec struct {
	// +optional
	Traffic *KDBProxyTrafficPolicySpec `json:"traffic,omitempty"`

	// +optional
	Backends *KDBProxyBackendPolicySpec `json:"backends,omitempty"`

	// +optional
	Users []KDBProxyUserSpec `json:"users,omitempty"`

	// +optional
	Parameters map[string]string `json:"parameters,omitempty"`

	// +optional
	Extensions map[string]apiextensionsv1.JSON `json:"extensions,omitempty"`
}

type KDBProxyTrafficPolicySpec struct {
	// +optional
	ReadWriteSplit *KDBProxyReadWriteSplitSpec `json:"readWriteSplit,omitempty"`

	// +optional
	LoadBalance *KDBProxyLoadBalanceSpec `json:"loadBalance,omitempty"`
}

type KDBProxyReadWriteSplitSpec struct {
	// +optional
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`
}

type KDBProxyLoadBalanceSpec struct {
	// +optional
	// +kubebuilder:validation:Enum=RoundRobin;LeastConn;Random;Weighted
	// +kubebuilder:default=RoundRobin
	Algorithm string `json:"algorithm,omitempty"`
}

type KDBProxyBackendPolicySpec struct {
	// +optional
	// +kubebuilder:validation:Enum=Operator;Static;ServiceDiscovery
	// +kubebuilder:default=Operator
	Discovery string `json:"discovery,omitempty"`

	// +optional
	HealthCheck *KDBProxyHealthCheckSpec `json:"healthCheck,omitempty"`
}

type KDBProxyHealthCheckSpec struct {
	// +optional
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// +optional
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	IntervalSeconds int32 `json:"intervalSeconds,omitempty"`
}

type KDBProxyUserSpec struct {
	// +optional
	Username string `json:"username,omitempty"`

	// +optional
	PasswordSecretRef *corev1.SecretKeySelector `json:"passwordSecretRef,omitempty"`

	// +optional
	DefaultRoute string `json:"defaultRoute,omitempty"`
}

type KDBProxyServiceSpec struct {
	// +optional
	// +kubebuilder:default=ClusterIP
	Type corev1.ServiceType `json:"type,omitempty"`

	// +optional
	// +kubebuilder:default=6033
	// +kubebuilder:validation:Minimum=1
	MysqlPort int32 `json:"mysqlPort,omitempty"`

	// +optional
	// +kubebuilder:default=6032
	// +kubebuilder:validation:Minimum=1
	AdminPort int32 `json:"adminPort,omitempty"`
}

type KDBProxyResourceSpec struct {
	// +optional
	ProxySQL corev1.ResourceRequirements `json:"proxysql,omitempty"`

	// +optional
	Mgr corev1.ResourceRequirements `json:"mgr,omitempty"`
}

type KDBProxyStatus struct {
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// +optional
	Type string `json:"type,omitempty"`

	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// +optional
	MysqlPort int32 `json:"mysqlPort,omitempty"`

	// +optional
	ConfigMapName string `json:"configMapName,omitempty"`

	// +optional
	ConfigVersion string `json:"configVersion,omitempty"`

	// +optional
	Hostgroups *KDBProxyHostgroupsStatus `json:"hostgroups,omitempty"`

	// +optional
	Backends []KDBProxyBackendStatus `json:"backends,omitempty"`
}

type KDBProxyHostgroupsStatus struct {
	// +optional
	Writer int32 `json:"writer,omitempty"`

	// +optional
	Reader int32 `json:"reader,omitempty"`
}

type KDBProxyBackendStatus struct {
	// +optional
	Host string `json:"host,omitempty"`

	// +optional
	Port int32 `json:"port,omitempty"`

	// +optional
	Role string `json:"role,omitempty"`

	// +optional
	Status string `json:"status,omitempty"`
}
