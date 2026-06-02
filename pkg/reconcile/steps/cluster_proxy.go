package steps

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/generate"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (s *ClusterStepManager) ReconcileProxySQL() kube.BindFunc {
	return s.StepBinder(
		"ReconcileProxySQL",
		func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
			cluster := rc.GetCluster()
			if !proxyEnabled(cluster) {
				if err := deleteProxySQLResources(rc); err != nil {
					return flow.Error(err, "delete proxysql resources err")
				}
				return flow.Pass()
			}
			if !strings.EqualFold(cluster.Spec.Engine, naming.MySQLEngine) {
				return flow.Error(fmt.Errorf("proxysql only supports engine=%s", naming.MySQLEngine), "invalid proxy engine")
			}
			if err := ensureProxySQLConfigMap(rc); err != nil {
				return flow.Error(err, "ensure proxysql configmap err")
			}
			if err := ensureProxySQLSecret(rc); err != nil {
				return flow.Error(err, "ensure proxysql secret err")
			}
			version, err := proxySQLConfigVersion(rc)
			if err != nil {
				return flow.Error(err, "get proxysql config version err")
			}
			if err := ensureProxySQLDeployment(rc, version); err != nil {
				return flow.Error(err, "ensure proxysql deployment err")
			}
			if err := ensureProxySQLService(rc); err != nil {
				return flow.Error(err, "ensure proxysql service err")
			}
			return flow.Pass()
		})
}

func (s *ClusterStepManager) PatchClusterStatus() kube.BindFunc {
	return s.StepBinder(
		"PatchClusterStatus",
		func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
			if err := updateProxyStatus(rc); err != nil {
				return flow.Error(err, "update proxy status err")
			}
			if err := rc.PatchKDBClusterStatus(); err != nil {
				return flow.Error(err, "patch cluster status err")
			}
			return flow.Pass()
		})
}

func ensureProxySQLConfigMap(rc *context.ClusterContext) error {
	cluster := rc.GetCluster()
	cm := &corev1.ConfigMap{ObjectMeta: naming.ProxySQLConfigMap(cluster)}
	cm.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
	if err := errors.WithStack(rc.SetControllerReference(cm)); err != nil {
		return err
	}
	_, _, err := generate.ProxySQLConfigMapIntent(cluster, cm)
	if err != nil {
		return err
	}
	return errors.WithStack(rc.Apply(cm))
}

func ensureProxySQLSecret(rc *context.ClusterContext) error {
	cluster := rc.GetCluster()
	secret := &corev1.Secret{ObjectMeta: naming.ProxySQLSecret(cluster)}
	err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(secret), secret)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return errors.WithStack(err)
	}
	secret = &corev1.Secret{ObjectMeta: naming.ProxySQLSecret(cluster)}
	secret.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
	secret.Type = corev1.SecretTypeOpaque
	secret.Labels = naming.Merge(cluster.Labels, naming.ProxySQLLabels(cluster))
	secret.Annotations = naming.Merge(cluster.Annotations)
	secret.Data = map[string][]byte{
		"admin-username":   []byte("admin"),
		"admin-password":   []byte(randomHex(24)),
		"monitor-username": []byte("monitor"),
		"monitor-password": []byte(randomHex(24)),
	}
	if err := errors.WithStack(rc.SetControllerReference(secret)); err != nil {
		return err
	}
	return errors.WithStack(rc.Client().Create(rc.Context(), secret))
}

func ensureProxySQLDeployment(rc *context.ClusterContext, configVersion string) error {
	cluster := rc.GetCluster()
	deploy := &appsv1.Deployment{ObjectMeta: naming.ProxySQLDeployment(cluster)}
	deploy.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("Deployment"))
	if err := errors.WithStack(rc.SetControllerReference(deploy)); err != nil {
		return err
	}
	generate.ProxySQLDeploymentIntent(cluster, deploy, configVersion)
	return errors.WithStack(rc.Apply(deploy))
}

func ensureProxySQLService(rc *context.ClusterContext) error {
	cluster := rc.GetCluster()
	service := &corev1.Service{ObjectMeta: naming.ProxySQLService(cluster)}
	service.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
	if err := errors.WithStack(rc.SetControllerReference(service)); err != nil {
		return err
	}
	generate.ProxySQLServiceIntent(cluster, service)
	return errors.WithStack(rc.Apply(service))
}

func deleteProxySQLResources(rc *context.ClusterContext) error {
	cluster := rc.GetCluster()
	for _, obj := range []client.Object{
		&appsv1.Deployment{ObjectMeta: naming.ProxySQLDeployment(cluster)},
		&corev1.Service{ObjectMeta: naming.ProxySQLService(cluster)},
	} {
		if err := client.IgnoreNotFound(rc.Client().Delete(rc.Context(), obj)); err != nil {
			return errors.WithStack(err)
		}
	}
	return nil
}

func updateProxyStatus(rc *context.ClusterContext) error {
	cluster := rc.GetCluster()
	if !proxyEnabled(cluster) {
		cluster.Status.Proxy = &v1.KDBProxyStatus{Enabled: false}
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:    v1.ProxyConditionAvailable,
			Status:  metav1.ConditionFalse,
			Reason:  "ProxyDisabled",
			Message: "ProxySQL is disabled",
		})
		return nil
	}

	proxy := generate.DefaultedProxySpec(cluster)
	mysqlPort, _ := generate.ProxyServicePorts(proxy)
	configVersion, _ := proxySQLConfigVersion(rc)
	backends, err := generate.ProxySQLBackends(cluster)
	if err != nil {
		return err
	}
	var readyReplicas int32
	deploy := &appsv1.Deployment{ObjectMeta: naming.ProxySQLDeployment(cluster)}
	if err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(deploy), deploy); err == nil {
		readyReplicas = deploy.Status.ReadyReplicas
	} else if !apierrors.IsNotFound(err) {
		return errors.WithStack(err)
	}

	replicas := generate.ProxyReplicas(proxy)
	cluster.Status.Proxy = &v1.KDBProxyStatus{
		Enabled:       true,
		Type:          proxy.Type,
		Replicas:      replicas,
		ReadyReplicas: readyReplicas,
		ServiceName:   naming.ProxySQLService(cluster).Name,
		MysqlPort:     mysqlPort,
		ConfigMapName: naming.ProxySQLConfigMap(cluster).Name,
		ConfigVersion: configVersion,
		Hostgroups: &v1.KDBProxyHostgroupsStatus{
			Writer: naming.ProxySQLWriterHostgroup,
			Reader: naming.ProxySQLReaderHostgroup,
		},
		Backends: backends,
	}
	condition := metav1.Condition{
		Type:   v1.ProxyConditionAvailable,
		Status: metav1.ConditionTrue,
		Reason: "ProxyReady",
	}
	if readyReplicas < replicas {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "ProxyPodsNotReady"
		condition.Message = fmt.Sprintf("ready replicas %d/%d", readyReplicas, replicas)
	}
	apimeta.SetStatusCondition(&cluster.Status.Conditions, condition)
	return nil
}

func proxySQLConfigVersion(rc *context.ClusterContext) (string, error) {
	cluster := rc.GetCluster()
	cm := &corev1.ConfigMap{ObjectMeta: naming.ProxySQLConfigMap(cluster)}
	if err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(cm), cm); err != nil {
		return "", errors.WithStack(err)
	}
	return cm.Data[naming.ProxySQLConfigVersionFileName], nil
}

func proxyEnabled(cluster *v1.KDBCluster) bool {
	return cluster != nil && cluster.Spec.Proxy != nil && cluster.Spec.Proxy.Enabled
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return generate.ProxySQLConfigVersion(fmt.Sprint(size), clusterFallbackSalt)
	}
	return hex.EncodeToString(buf)
}

const clusterFallbackSalt = "kdb-proxysql-secret"
