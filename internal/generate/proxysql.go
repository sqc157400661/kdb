package generate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/topology"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/yaml"
)

const (
	defaultProxySQLImage                = "proxysql/proxysql:3.0.2"
	defaultProxyMgrImage                = "kdb-sidecar:latest"
	proxySQLAdminCredentialsPlaceholder = "__KDB_ADMIN_CREDENTIALS__"
)

type ProxySQLDesiredConfig struct {
	Cluster  string                      `json:"cluster"`
	Proxy    ProxySQLDesiredProxy        `json:"proxy"`
	Backends []v1.KDBProxyBackendStatus  `json:"backends"`
	Users    []ProxySQLDesiredUserConfig `json:"users,omitempty"`
}

type ProxySQLDesiredProxy struct {
	Type       string                 `json:"type"`
	Traffic    ProxySQLTrafficConfig  `json:"traffic"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

type ProxySQLTrafficConfig struct {
	ReadWriteSplit map[string]bool   `json:"read_write_split"`
	LoadBalance    map[string]string `json:"load_balance"`
}

type ProxySQLDesiredUserConfig struct {
	Username     string `json:"username"`
	DefaultRoute string `json:"default_route"`
}

func ProxySQLConfigMapIntent(cluster *v1.KDBCluster, cm *corev1.ConfigMap) (string, []v1.KDBProxyBackendStatus, error) {
	desired, backends, err := RenderProxySQLDesiredConfig(cluster)
	if err != nil {
		return "", nil, err
	}
	proxysqlConf := RenderProxySQLConfig(cluster)
	version := ProxySQLConfigVersion(desired, proxysqlConf)

	cm.Labels = naming.Merge(cluster.Labels, naming.ProxySQLLabels(cluster))
	cm.Annotations = naming.Merge(cluster.Annotations)
	cm.Data = map[string]string{
		naming.ProxySQLConfigVersionFileName: version,
		naming.ProxySQLDesiredFileName:       naming.YamlGeneratedWarning + desired,
		naming.ProxySQLConfigFileName:        naming.YamlGeneratedWarning + proxysqlConf,
	}
	return version, backends, nil
}

func ProxySQLInstanceConfigMapIntent(instance *v1.KDBInstance, cm *corev1.ConfigMap) (string, []v1.KDBProxyBackendStatus, error) {
	desired, backends, err := RenderProxySQLInstanceDesiredConfig(instance)
	if err != nil {
		return "", nil, err
	}
	proxysqlConf := RenderProxySQLInstanceConfig(instance)
	version := ProxySQLConfigVersion(desired, proxysqlConf)

	cm.Labels = naming.Merge(instance.Labels, naming.ProxySQLInstanceLabels(instance))
	cm.Annotations = naming.Merge(instance.Annotations)
	cm.Data = map[string]string{
		naming.ProxySQLConfigVersionFileName: version,
		naming.ProxySQLDesiredFileName:       naming.YamlGeneratedWarning + desired,
		naming.ProxySQLConfigFileName:        naming.YamlGeneratedWarning + proxysqlConf,
	}
	return version, backends, nil
}

func ProxySQLDeploymentIntent(cluster *v1.KDBCluster, deploy *appsv1.Deployment, configVersion string) {
	proxy := DefaultedProxySpec(cluster)
	labels := naming.Merge(cluster.Labels, naming.ProxySQLLabels(cluster))
	replicas := ProxyReplicas(proxy)
	mysqlPort, adminPort := ProxyServicePorts(proxy)

	deploy.Labels = labels
	deploy.Annotations = naming.Merge(cluster.Annotations)
	deploy.Spec.Replicas = util.Int32(replicas)
	deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: naming.ProxySQLLabels(cluster)}
	deploy.Spec.Strategy = appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
		},
	}
	deploy.Spec.Template.Labels = labels
	deploy.Spec.Template.Annotations = naming.Merge(cluster.Annotations, map[string]string{
		"checksum/proxysql-config": configVersion,
	})
	deploy.Spec.Template.Spec.ShareProcessNamespace = util.Bool(true)
	deploy.Spec.Template.Spec.EnableServiceLinks = util.Bool(false)
	deploy.Spec.Template.Spec.Containers = []corev1.Container{
		{
			Name:    "proxysql",
			Image:   proxy.Image,
			Command: []string{"/bin/sh", "-c"},
			Args:    []string{proxySQLStartCommand()},
			Ports: []corev1.ContainerPort{
				{Name: "mysql", ContainerPort: mysqlPort},
				{Name: "admin", ContainerPort: adminPort},
			},
			Resources: proxy.Resources.ProxySQL,
			VolumeMounts: []corev1.VolumeMount{
				{Name: naming.ProxySQLConfigVolume, MountPath: naming.ProxySQLConfigMountPath, ReadOnly: true},
				{Name: naming.ProxySQLSecretVolume, MountPath: naming.ProxySQLSecretMountPath, ReadOnly: true},
				{Name: naming.ProxySQLDataVolume, MountPath: naming.ProxySQLDataMountPath},
				{Name: naming.ProxySQLRuntimeVolume, MountPath: naming.ProxySQLRuntimeMountPath},
			},
		},
		{
			Name:    "mgr",
			Image:   proxy.MgrImage,
			Command: []string{"/kdb/bin/manager"},
			Args: []string{
				"ProxySQLMgr",
				"-c",
				naming.ProxySQLConfigMountPath + "/" + naming.ProxySQLDesiredFileName,
			},
			Env: []corev1.EnvVar{
				{Name: "PROXYSQL_ADMIN_ADDR", Value: "127.0.0.1:" + strconv.Itoa(int(adminPort))},
				{Name: "PROXYSQL_CONFIG_VERSION_FILE", Value: naming.ProxySQLConfigMountPath + "/" + naming.ProxySQLConfigVersionFileName},
				{Name: "PROXYSQL_SECRET_DIR", Value: naming.ProxySQLSecretMountPath},
				{Name: "PROXYSQL_START_COMMAND", Value: "/bin/true"},
			},
			Ports: []corev1.ContainerPort{
				{Name: "mgr-http", ContainerPort: 8080},
				{Name: "metrics", ContainerPort: 9104},
			},
			Resources: proxy.Resources.Mgr,
			VolumeMounts: []corev1.VolumeMount{
				{Name: naming.ProxySQLConfigVolume, MountPath: naming.ProxySQLConfigMountPath, ReadOnly: true},
				{Name: naming.ProxySQLSecretVolume, MountPath: naming.ProxySQLSecretMountPath, ReadOnly: true},
				{Name: naming.ProxySQLRuntimeVolume, MountPath: naming.ProxySQLRuntimeMountPath},
			},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/health", Port: intstr.FromInt(8080)}},
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/v1/proxysql/status", Port: intstr.FromInt(8080)}},
			},
		},
	}
	deploy.Spec.Template.Spec.Volumes = proxySQLVolumes(
		naming.ProxySQLConfigMap(cluster).Name,
		naming.ProxySQLSecret(cluster).Name,
		proxy.Config.Inline.Users,
	)
}

func ProxySQLInstanceDeploymentIntent(instance *v1.KDBInstance, deploy *appsv1.Deployment, configVersion string) {
	proxy := DefaultedProxySpecForSpec(instance.Spec.Proxy)
	labels := naming.Merge(instance.Labels, naming.ProxySQLInstanceLabels(instance))
	replicas := ProxyReplicas(proxy)
	mysqlPort, adminPort := ProxyServicePorts(proxy)

	deploy.Labels = labels
	deploy.Annotations = naming.Merge(instance.Annotations)
	deploy.Spec.Replicas = util.Int32(replicas)
	deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: naming.ProxySQLInstanceLabels(instance)}
	deploy.Spec.Strategy = appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
		},
	}
	deploy.Spec.Template.Labels = labels
	deploy.Spec.Template.Annotations = naming.Merge(instance.Annotations, map[string]string{
		"checksum/proxysql-config": configVersion,
	})
	deploy.Spec.Template.Spec.ShareProcessNamespace = util.Bool(true)
	deploy.Spec.Template.Spec.EnableServiceLinks = util.Bool(false)
	deploy.Spec.Template.Spec.Containers = proxySQLContainers(proxy, mysqlPort, adminPort)
	deploy.Spec.Template.Spec.Volumes = proxySQLVolumes(
		naming.ProxySQLInstanceConfigMap(instance).Name,
		naming.ProxySQLInstanceSecret(instance).Name,
		proxy.Config.Inline.Users,
	)
}

func ProxySQLServiceIntent(cluster *v1.KDBCluster, service *corev1.Service) {
	proxy := DefaultedProxySpec(cluster)
	mysqlPort, _ := ProxyServicePorts(proxy)
	labels := naming.Merge(cluster.Labels, naming.ProxySQLLabels(cluster))

	service.Labels = labels
	service.Annotations = naming.Merge(cluster.Annotations)
	service.Spec.Type = proxy.Service.Type
	service.Spec.Selector = naming.ProxySQLLabels(cluster)
	service.Spec.Ports = []corev1.ServicePort{{
		Name:       "mysql",
		Port:       mysqlPort,
		TargetPort: intstr.FromString("mysql"),
	}}
}

func ProxySQLInstanceServiceIntent(instance *v1.KDBInstance, service *corev1.Service) {
	proxy := DefaultedProxySpecForSpec(instance.Spec.Proxy)
	mysqlPort, _ := ProxyServicePorts(proxy)
	labels := naming.Merge(instance.Labels, naming.ProxySQLInstanceLabels(instance))

	service.Labels = labels
	service.Annotations = naming.Merge(instance.Annotations)
	service.Spec.Type = proxy.Service.Type
	service.Spec.Selector = naming.ProxySQLInstanceLabels(instance)
	service.Spec.Ports = []corev1.ServicePort{{
		Name:       "mysql",
		Port:       mysqlPort,
		TargetPort: intstr.FromString("mysql"),
	}}
}

func DefaultProxySQLSecretData() map[string][]byte {
	return map[string][]byte{
		"admin-username":   []byte("admin"),
		"admin-password":   []byte(randomHex(24)),
		"monitor-username": []byte("monitor"),
		"monitor-password": []byte(randomHex(24)),
	}
}

func RenderProxySQLDesiredConfig(cluster *v1.KDBCluster) (string, []v1.KDBProxyBackendStatus, error) {
	proxy := DefaultedProxySpec(cluster)
	backends, err := ProxySQLBackends(cluster)
	if err != nil {
		return "", nil, err
	}
	users := make([]ProxySQLDesiredUserConfig, 0, len(proxy.Config.Inline.Users))
	for _, user := range proxy.Config.Inline.Users {
		if err := validateProxySQLUser(user); err != nil {
			return "", nil, err
		}
		users = append(users, ProxySQLDesiredUserConfig{
			Username:     strings.TrimSpace(user.Username),
			DefaultRoute: defaultString(user.DefaultRoute, "writer"),
		})
	}

	desired := ProxySQLDesiredConfig{
		Cluster: cluster.Name,
		Proxy: ProxySQLDesiredProxy{
			Type: proxy.Type,
			Traffic: ProxySQLTrafficConfig{
				ReadWriteSplit: map[string]bool{"enabled": proxy.Config.Inline.Traffic.ReadWriteSplit.Enabled},
				LoadBalance:    map[string]string{"algorithm": proxy.Config.Inline.Traffic.LoadBalance.Algorithm},
			},
			Extensions: proxysqlExtensions(proxy),
		},
		Backends: backends,
		Users:    users,
	}
	out, err := yaml.Marshal(desired)
	if err != nil {
		return "", nil, err
	}
	return string(out), backends, nil
}

func RenderProxySQLInstanceDesiredConfig(instance *v1.KDBInstance) (string, []v1.KDBProxyBackendStatus, error) {
	proxy := DefaultedProxySpecForSpec(instance.Spec.Proxy)
	backends, err := ProxySQLInstanceBackends(instance)
	if err != nil {
		return "", nil, err
	}
	users := make([]ProxySQLDesiredUserConfig, 0, len(proxy.Config.Inline.Users))
	for _, user := range proxy.Config.Inline.Users {
		if err := validateProxySQLUser(user); err != nil {
			return "", nil, err
		}
		users = append(users, ProxySQLDesiredUserConfig{
			Username:     strings.TrimSpace(user.Username),
			DefaultRoute: defaultString(user.DefaultRoute, "writer"),
		})
	}

	desired := ProxySQLDesiredConfig{
		Cluster: instance.Name,
		Proxy: ProxySQLDesiredProxy{
			Type: proxy.Type,
			Traffic: ProxySQLTrafficConfig{
				ReadWriteSplit: map[string]bool{"enabled": proxy.Config.Inline.Traffic.ReadWriteSplit.Enabled},
				LoadBalance:    map[string]string{"algorithm": proxy.Config.Inline.Traffic.LoadBalance.Algorithm},
			},
			Extensions: proxysqlExtensions(proxy),
		},
		Backends: backends,
		Users:    users,
	}
	out, err := yaml.Marshal(desired)
	if err != nil {
		return "", nil, err
	}
	return string(out), backends, nil
}

func RenderProxySQLConfig(cluster *v1.KDBCluster) string {
	proxy := DefaultedProxySpec(cluster)
	return renderProxySQLConfig(proxy)
}

func RenderProxySQLInstanceConfig(instance *v1.KDBInstance) string {
	proxy := DefaultedProxySpecForSpec(instance.Spec.Proxy)
	return renderProxySQLConfig(proxy)
}

func renderProxySQLConfig(proxy v1.KDBProxySpec) string {
	mysqlPort, adminPort := ProxyServicePorts(proxy)
	var mysqlVariables []string
	mysqlVariables = append(mysqlVariables,
		"threads=4",
		"max_connections=2048",
		fmt.Sprintf("interfaces=\"0.0.0.0:%d\"", mysqlPort),
	)
	if proxy.Config.Inline.Parameters != nil {
		keys := make([]string, 0, len(proxy.Config.Inline.Parameters))
		for k := range proxy.Config.Inline.Parameters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			mysqlVariables = append(mysqlVariables, fmt.Sprintf("%s=%q", k, proxy.Config.Inline.Parameters[k]))
		}
	}

	return fmt.Sprintf(`datadir="%s"
admin_variables=
{
  admin_credentials="%s"
  mysql_ifaces="0.0.0.0:%d"
}
mysql_variables=
{
  %s
}
`, naming.ProxySQLDataMountPath, proxySQLAdminCredentialsPlaceholder, adminPort, strings.Join(mysqlVariables, "\n  "))
}

func ProxySQLConfigVersion(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n---\n")))
	return hex.EncodeToString(sum[:])[:16]
}

func ProxySQLBackends(cluster *v1.KDBCluster) ([]v1.KDBProxyBackendStatus, error) {
	if cluster == nil {
		return nil, nil
	}
	plan, err := topology.ResolveClusterPlan(cluster)
	if err != nil {
		return nil, err
	}
	backends := make([]v1.KDBProxyBackendStatus, 0, len(cluster.Spec.Instances))
	for i, ins := range cluster.Spec.Instances {
		port := naming.GetPortByEngine(cluster.Spec.Engine)
		if ins.Port != nil {
			port = *ins.Port
		}
		role := "reader"
		if i == plan.PrimaryIndex {
			role = "writer"
		}
		backends = append(backends, v1.KDBProxyBackendStatus{
			Host:   naming.InstancePodHost(ins.Name, naming.InstancePodServiceName(ins.Name), cluster.Namespace, 0),
			Port:   port,
			Role:   role,
			Status: "ONLINE",
		})
	}
	return backends, nil
}

func ProxySQLInstanceBackends(instance *v1.KDBInstance) ([]v1.KDBProxyBackendStatus, error) {
	if instance == nil || instance.Spec.InstanceSet.Replicas == nil {
		return nil, nil
	}
	plan, err := topology.ResolveInstancePlan(instance)
	if err != nil {
		return nil, err
	}
	replicas := int(*instance.Spec.InstanceSet.Replicas)
	port := naming.KDBInstanceMasterPort(instance)
	serviceName := naming.InstancePodServiceName(instance.Name)
	backends := make([]v1.KDBProxyBackendStatus, 0, replicas)
	for i := 0; i < replicas; i++ {
		role := "reader"
		if i == plan.Primary {
			role = "writer"
		}
		backends = append(backends, v1.KDBProxyBackendStatus{
			Host:   naming.InstancePodHost(instance.Name, serviceName, instance.Namespace, i),
			Port:   port,
			Role:   role,
			Status: "ONLINE",
		})
	}
	return backends, nil
}

func DefaultedProxySpec(cluster *v1.KDBCluster) v1.KDBProxySpec {
	if cluster != nil && cluster.Spec.Proxy != nil {
		return DefaultedProxySpecForSpec(cluster.Spec.Proxy)
	}
	return DefaultedProxySpecForSpec(nil)
}

func DefaultedProxySpecForSpec(spec *v1.KDBProxySpec) v1.KDBProxySpec {
	proxy := v1.KDBProxySpec{}
	if spec != nil {
		proxy = *spec
	}
	if proxy.Type == "" {
		proxy.Type = v1.ProxyTypeProxySQL
	}
	if proxy.Replicas == nil {
		proxy.Replicas = util.Int32(2)
	}
	if proxy.Image == "" {
		proxy.Image = defaultProxySQLImage
	}
	if proxy.MgrImage == "" {
		proxy.MgrImage = defaultProxyMgrImage
	}
	if proxy.Service == nil {
		proxy.Service = &v1.KDBProxyServiceSpec{}
	}
	if proxy.Service.Type == "" {
		proxy.Service.Type = corev1.ServiceTypeClusterIP
	}
	if proxy.Service.MysqlPort == 0 {
		proxy.Service.MysqlPort = 6033
	}
	if proxy.Service.AdminPort == 0 {
		proxy.Service.AdminPort = 6032
	}
	if proxy.Config == nil {
		proxy.Config = &v1.KDBProxyConfigSpec{}
	}
	if proxy.Config.Source == "" {
		proxy.Config.Source = v1.ProxyConfigSourceInline
	}
	if proxy.Config.Inline == nil {
		proxy.Config.Inline = &v1.KDBProxyInlineConfigSpec{}
	}
	if proxy.Config.Inline.Traffic == nil {
		proxy.Config.Inline.Traffic = &v1.KDBProxyTrafficPolicySpec{}
	}
	if proxy.Config.Inline.Traffic.ReadWriteSplit == nil {
		proxy.Config.Inline.Traffic.ReadWriteSplit = &v1.KDBProxyReadWriteSplitSpec{}
	}
	if proxy.Config.Inline.Traffic.LoadBalance == nil {
		proxy.Config.Inline.Traffic.LoadBalance = &v1.KDBProxyLoadBalanceSpec{}
	}
	if proxy.Config.Inline.Traffic.LoadBalance.Algorithm == "" {
		proxy.Config.Inline.Traffic.LoadBalance.Algorithm = v1.ProxyLoadBalanceRoundRobin
	}
	if proxy.Config.Inline.Backends == nil {
		proxy.Config.Inline.Backends = &v1.KDBProxyBackendPolicySpec{}
	}
	if proxy.Config.Inline.Backends.Discovery == "" {
		proxy.Config.Inline.Backends.Discovery = v1.ProxyBackendDiscoveryOperator
	}
	if proxy.Resources == nil {
		proxy.Resources = &v1.KDBProxyResourceSpec{}
	}
	return proxy
}

func proxySQLContainers(proxy v1.KDBProxySpec, mysqlPort, adminPort int32) []corev1.Container {
	return []corev1.Container{
		{
			Name:    "proxysql",
			Image:   proxy.Image,
			Command: []string{"/bin/sh", "-c"},
			Args:    []string{proxySQLStartCommand()},
			Ports: []corev1.ContainerPort{
				{Name: "mysql", ContainerPort: mysqlPort},
				{Name: "admin", ContainerPort: adminPort},
			},
			Resources: proxy.Resources.ProxySQL,
			VolumeMounts: []corev1.VolumeMount{
				{Name: naming.ProxySQLConfigVolume, MountPath: naming.ProxySQLConfigMountPath, ReadOnly: true},
				{Name: naming.ProxySQLSecretVolume, MountPath: naming.ProxySQLSecretMountPath, ReadOnly: true},
				{Name: naming.ProxySQLDataVolume, MountPath: naming.ProxySQLDataMountPath},
				{Name: naming.ProxySQLRuntimeVolume, MountPath: naming.ProxySQLRuntimeMountPath},
			},
		},
		{
			Name:    "mgr",
			Image:   proxy.MgrImage,
			Command: []string{"/kdb/bin/manager"},
			Args: []string{
				"ProxySQLMgr",
				"-c",
				naming.ProxySQLConfigMountPath + "/" + naming.ProxySQLDesiredFileName,
			},
			Env: []corev1.EnvVar{
				{Name: "PROXYSQL_ADMIN_ADDR", Value: "127.0.0.1:" + strconv.Itoa(int(adminPort))},
				{Name: "PROXYSQL_CONFIG_VERSION_FILE", Value: naming.ProxySQLConfigMountPath + "/" + naming.ProxySQLConfigVersionFileName},
				{Name: "PROXYSQL_SECRET_DIR", Value: naming.ProxySQLSecretMountPath},
				{Name: "PROXYSQL_START_COMMAND", Value: "/bin/true"},
			},
			Ports: []corev1.ContainerPort{
				{Name: "mgr-http", ContainerPort: 8080},
				{Name: "metrics", ContainerPort: 9104},
			},
			Resources: proxy.Resources.Mgr,
			VolumeMounts: []corev1.VolumeMount{
				{Name: naming.ProxySQLConfigVolume, MountPath: naming.ProxySQLConfigMountPath, ReadOnly: true},
				{Name: naming.ProxySQLSecretVolume, MountPath: naming.ProxySQLSecretMountPath, ReadOnly: true},
				{Name: naming.ProxySQLRuntimeVolume, MountPath: naming.ProxySQLRuntimeMountPath},
			},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/health", Port: intstr.FromInt(8080)}},
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/v1/proxysql/status", Port: intstr.FromInt(8080)}},
			},
		},
	}
}

func proxySQLStartCommand() string {
	configFile := naming.ProxySQLConfigMountPath + "/" + naming.ProxySQLConfigFileName
	runtimeConfigFile := naming.ProxySQLRuntimeMountPath + "/" + naming.ProxySQLConfigFileName
	adminUsernameFile := naming.ProxySQLSecretMountPath + "/admin-username"
	adminPasswordFile := naming.ProxySQLSecretMountPath + "/admin-password"
	return fmt.Sprintf(`set -eu
umask 077
admin_username="$(tr -d '\r\n' < %s)"
admin_password="$(tr -d '\r\n' < %s)"
case "${admin_username}" in ""|*[!A-Za-z0-9_.-]*) echo "invalid ProxySQL admin username" >&2; exit 1;; esac
case "${admin_password}" in ""|*[!A-Za-z0-9_.-]*) echo "invalid ProxySQL admin password" >&2; exit 1;; esac
cp %s %s
sed -i "s#%s#${admin_username}:${admin_password}#g" %s
exec proxysql -f -c %s`, adminUsernameFile, adminPasswordFile, configFile, runtimeConfigFile,
		proxySQLAdminCredentialsPlaceholder, runtimeConfigFile, runtimeConfigFile)
}

func proxySQLVolumes(configMapName, secretName string, users []v1.KDBProxyUserSpec) []corev1.Volume {
	secretSources := []corev1.VolumeProjection{{
		Secret: &corev1.SecretProjection{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
		},
	}}
	for _, user := range users {
		username := strings.TrimSpace(user.Username)
		if username == "" || user.PasswordSecretRef == nil {
			continue
		}
		secretSources = append(secretSources, corev1.VolumeProjection{
			Secret: &corev1.SecretProjection{
				LocalObjectReference: user.PasswordSecretRef.LocalObjectReference,
				Items: []corev1.KeyToPath{{
					Key:  user.PasswordSecretRef.Key,
					Path: "users/" + username + "/password",
				}},
				Optional: user.PasswordSecretRef.Optional,
			},
		})
	}
	return []corev1.Volume{
		{
			Name: naming.ProxySQLConfigVolume,
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
			}},
		},
		{
			Name:         naming.ProxySQLSecretVolume,
			VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: secretSources}},
		},
		{Name: naming.ProxySQLDataVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: naming.ProxySQLRuntimeVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}
}

func validateProxySQLUser(user v1.KDBProxyUserSpec) error {
	username := strings.TrimSpace(user.Username)
	if username == "" {
		return fmt.Errorf("proxysql user username is empty")
	}
	for _, char := range username {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return fmt.Errorf("proxysql user %q contains a character unsupported by Secret projection paths", username)
	}
	if user.PasswordSecretRef == nil || strings.TrimSpace(user.PasswordSecretRef.Name) == "" || strings.TrimSpace(user.PasswordSecretRef.Key) == "" {
		return fmt.Errorf("proxysql user %q requires passwordSecretRef.name and passwordSecretRef.key", username)
	}
	return nil
}

func ProxyReplicas(proxy v1.KDBProxySpec) int32 {
	if proxy.Replicas == nil || *proxy.Replicas < 1 {
		return 1
	}
	return *proxy.Replicas
}

func ProxyServicePorts(proxy v1.KDBProxySpec) (int32, int32) {
	mysqlPort := proxy.Service.MysqlPort
	adminPort := proxy.Service.AdminPort
	if mysqlPort == 0 {
		mysqlPort = 6033
	}
	if adminPort == 0 {
		adminPort = 6032
	}
	return mysqlPort, adminPort
}

func proxysqlExtensions(proxy v1.KDBProxySpec) map[string]interface{} {
	out := map[string]interface{}{
		"proxysql": map[string]interface{}{
			"hostgroups": map[string]int32{
				"writer":  naming.ProxySQLWriterHostgroup,
				"reader":  naming.ProxySQLReaderHostgroup,
				"offline": naming.ProxySQLOfflineHostgroup,
			},
		},
	}
	if len(proxy.Config.Inline.Extensions) == 0 {
		return out
	}
	for k, v := range proxy.Config.Inline.Extensions {
		out[k] = v
	}
	return out
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return ProxySQLConfigVersion(strconv.Itoa(size)) + ProxySQLConfigVersion(fmt.Sprint(size))
	}
	return hex.EncodeToString(buf)
}
