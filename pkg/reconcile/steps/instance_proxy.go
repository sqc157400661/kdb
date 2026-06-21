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

func (s *InstanceStepManager) ReconcileProxySQL() kube.BindFunc {
	return s.StepBinder(
		"ReconcileProxySQL",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			if !instanceProxyEnabled(instance) {
				if err := deleteInstanceProxySQLResources(rc); err != nil {
					return flow.Error(err, "delete proxysql resources err")
				}
				updateInstanceProxyDisabledStatus(instance)
				return flow.Pass()
			}
			if !naming.IsMySQLEngine(instance) {
				return flow.Error(fmt.Errorf("proxysql only supports engine=%s", naming.MySQLEngine), "invalid proxy engine")
			}
			if err := ensureInstanceProxySQLConfigMap(rc); err != nil {
				return flow.Error(err, "ensure proxysql configmap err")
			}
			if err := ensureInstanceProxySQLSecret(rc); err != nil {
				return flow.Error(err, "ensure proxysql secret err")
			}
			version, err := instanceProxySQLConfigVersion(rc)
			if err != nil {
				return flow.Error(err, "get proxysql config version err")
			}
			if err := ensureInstanceProxySQLDeployment(rc, version); err != nil {
				return flow.Error(err, "ensure proxysql deployment err")
			}
			if err := ensureInstanceProxySQLService(rc); err != nil {
				return flow.Error(err, "ensure proxysql service err")
			}
			if err := updateInstanceProxyStatus(rc); err != nil {
				return flow.Error(err, "update proxysql status err")
			}
			return flow.Pass()
		})
}

func ensureInstanceProxySQLConfigMap(rc *context.InstanceContext) error {
	instance := rc.GetInstance()
	cm := &corev1.ConfigMap{ObjectMeta: naming.ProxySQLInstanceConfigMap(instance)}
	cm.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
	if err := errors.WithStack(rc.SetControllerReference(cm)); err != nil {
		return err
	}
	_, _, err := generate.ProxySQLInstanceConfigMapIntent(instance, cm)
	if err != nil {
		return err
	}
	return errors.WithStack(rc.Apply(cm))
}

func ensureInstanceProxySQLSecret(rc *context.InstanceContext) error {
	instance := rc.GetInstance()
	secret := &corev1.Secret{ObjectMeta: naming.ProxySQLInstanceSecret(instance)}
	err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(secret), secret)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return errors.WithStack(err)
	}
	secret = &corev1.Secret{ObjectMeta: naming.ProxySQLInstanceSecret(instance)}
	secret.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
	secret.Type = corev1.SecretTypeOpaque
	secret.Labels = naming.Merge(instance.Labels, naming.ProxySQLInstanceLabels(instance))
	secret.Annotations = naming.Merge(instance.Annotations)
	secret.Data = map[string][]byte{
		"admin-username":   []byte("admin"),
		"admin-password":   []byte(randomInstanceProxyHex(24)),
		"monitor-username": []byte("monitor"),
		"monitor-password": []byte(randomInstanceProxyHex(24)),
	}
	if err := errors.WithStack(rc.SetControllerReference(secret)); err != nil {
		return err
	}
	return errors.WithStack(rc.Client().Create(rc.Context(), secret))
}

func ensureInstanceProxySQLDeployment(rc *context.InstanceContext, configVersion string) error {
	instance := rc.GetInstance()
	deploy := &appsv1.Deployment{ObjectMeta: naming.ProxySQLInstanceDeployment(instance)}
	deploy.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("Deployment"))
	if err := errors.WithStack(rc.SetControllerReference(deploy)); err != nil {
		return err
	}
	generate.ProxySQLInstanceDeploymentIntent(instance, deploy, configVersion)
	return errors.WithStack(rc.Apply(deploy))
}

func ensureInstanceProxySQLService(rc *context.InstanceContext) error {
	instance := rc.GetInstance()
	service := &corev1.Service{ObjectMeta: naming.ProxySQLInstanceService(instance)}
	service.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
	if err := errors.WithStack(rc.SetControllerReference(service)); err != nil {
		return err
	}
	generate.ProxySQLInstanceServiceIntent(instance, service)
	return errors.WithStack(rc.Apply(service))
}

func deleteInstanceProxySQLResources(rc *context.InstanceContext) error {
	instance := rc.GetInstance()
	for _, obj := range []client.Object{
		&appsv1.Deployment{ObjectMeta: naming.ProxySQLInstanceDeployment(instance)},
		&corev1.Service{ObjectMeta: naming.ProxySQLInstanceService(instance)},
		&corev1.ConfigMap{ObjectMeta: naming.ProxySQLInstanceConfigMap(instance)},
		&corev1.Secret{ObjectMeta: naming.ProxySQLInstanceSecret(instance)},
	} {
		if err := client.IgnoreNotFound(rc.Client().Delete(rc.Context(), obj)); err != nil {
			return errors.WithStack(err)
		}
	}
	return nil
}

func updateInstanceProxyStatus(rc *context.InstanceContext) error {
	instance := rc.GetInstance()
	if !instanceProxyEnabled(instance) {
		updateInstanceProxyDisabledStatus(instance)
		return nil
	}
	proxy := generate.DefaultedProxySpecForSpec(instance.Spec.Proxy)
	mysqlPort, _ := generate.ProxyServicePorts(proxy)
	configVersion, _ := instanceProxySQLConfigVersion(rc)
	backends, err := generate.ProxySQLInstanceBackends(instance)
	if err != nil {
		return err
	}
	var readyReplicas int32
	deploy := &appsv1.Deployment{ObjectMeta: naming.ProxySQLInstanceDeployment(instance)}
	if err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(deploy), deploy); err == nil {
		readyReplicas = deploy.Status.ReadyReplicas
	} else if !apierrors.IsNotFound(err) {
		return errors.WithStack(err)
	}

	replicas := generate.ProxyReplicas(proxy)
	instance.Status.Proxy = &v1.KDBProxyStatus{
		Enabled:       true,
		Type:          proxy.Type,
		Replicas:      replicas,
		ReadyReplicas: readyReplicas,
		ServiceName:   naming.ProxySQLInstanceService(instance).Name,
		MysqlPort:     mysqlPort,
		ConfigMapName: naming.ProxySQLInstanceConfigMap(instance).Name,
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
	apimeta.SetStatusCondition(&instance.Status.Conditions, condition)
	return nil
}

func updateInstanceProxyDisabledStatus(instance *v1.KDBInstance) {
	instance.Status.Proxy = &v1.KDBProxyStatus{Enabled: false}
	apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:    v1.ProxyConditionAvailable,
		Status:  metav1.ConditionFalse,
		Reason:  "ProxyDisabled",
		Message: "ProxySQL is disabled",
	})
}

func instanceProxySQLConfigVersion(rc *context.InstanceContext) (string, error) {
	instance := rc.GetInstance()
	cm := &corev1.ConfigMap{ObjectMeta: naming.ProxySQLInstanceConfigMap(instance)}
	if err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(cm), cm); err != nil {
		return "", errors.WithStack(err)
	}
	return cm.Data[naming.ProxySQLConfigVersionFileName], nil
}

func instanceProxyEnabled(instance *v1.KDBInstance) bool {
	return instance != nil && instance.Spec.Proxy != nil && instance.Spec.Proxy.Enabled &&
		strings.EqualFold(defaultInstanceProxyType(instance.Spec.Proxy.Type), v1.ProxyTypeProxySQL)
}

func defaultInstanceProxyType(value string) string {
	if strings.TrimSpace(value) == "" {
		return v1.ProxyTypeProxySQL
	}
	return strings.TrimSpace(value)
}

func randomInstanceProxyHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return generate.ProxySQLConfigVersion(fmt.Sprint(size), "kdb-instance-proxysql-secret")
	}
	return hex.EncodeToString(buf)
}
