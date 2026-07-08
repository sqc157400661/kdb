package clickhouse

import (
	"fmt"

	"github.com/pkg/errors"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/security"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const gatewayConfigVolumeName = "gateway-config"

func reconcileGateway(rc *context.InstanceContext) error {
	instance := rc.GetInstance()
	if !gatewayEnabled(instance) {
		if err := deleteGatewayResources(rc); err != nil {
			return err
		}
		setGatewayReadyCondition(instance, metav1.ConditionFalse, "GatewayDisabled", "ClickHouse Gateway is disabled", 0)
		return nil
	}
	if instance.Spec.ClickHouse.Gateway.BindingSecretRef == nil {
		setGatewayReadyCondition(instance, metav1.ConditionFalse, "BindingInvalid", "gateway bindingSecretRef is required", 0)
		return fmt.Errorf("gateway bindingSecretRef is required")
	}
	secret := &corev1.Secret{}
	if err := rc.Client().Get(rc.Context(), types.NamespacedName{Namespace: instance.Namespace, Name: instance.Spec.ClickHouse.Gateway.BindingSecretRef.Name}, secret); err != nil {
		setGatewayReadyCondition(instance, metav1.ConditionFalse, "BindingInvalid", err.Error(), 0)
		return err
	}
	bindings, err := parseGatewayBindingSecret(secret, instance)
	if err != nil {
		setGatewayReadyCondition(instance, metav1.ConditionFalse, "BindingInvalid", err.Error(), 0)
		return err
	}
	for _, obj := range []client.Object{
		buildGatewayConfigMap(instance, bindings),
		buildGatewayDeployment(instance, secret.Name, gatewayConfigRevision(instance, bindings, secret)),
		buildGatewayService(instance),
	} {
		if err = errors.WithStack(rc.SetControllerReference(obj)); err != nil {
			return err
		}
		if err = errors.WithStack(rc.Apply(obj)); err != nil {
			return err
		}
	}
	actual := &appsv1.Deployment{}
	if err = rc.Client().Get(rc.Context(), types.NamespacedName{Namespace: instance.Namespace, Name: naming.ClickHouseGatewayDeploymentName(instance.Name)}, actual); err != nil {
		setGatewayReadyCondition(instance, metav1.ConditionFalse, "GatewayUnavailable", err.Error(), 0)
		return err
	}
	if actual.Status.ReadyReplicas < desiredGatewayReplicas(instance) {
		setGatewayReadyCondition(instance, metav1.ConditionFalse, "GatewayStarting", "waiting for ClickHouse Gateway replicas", actual.Status.ReadyReplicas)
		return nil
	}
	setGatewayReadyCondition(instance, metav1.ConditionTrue, "GatewayReady", "ClickHouse Gateway replicas are ready", actual.Status.ReadyReplicas)
	return nil
}

func deleteGatewayResources(rc *context.InstanceContext) error {
	instance := rc.GetInstance()
	for _, obj := range []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: instance.Namespace, Name: naming.ClickHouseGatewayDeploymentName(instance.Name)}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: instance.Namespace, Name: naming.ClickHouseGatewayServiceName(instance.Name)}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: instance.Namespace, Name: naming.ClickHouseGatewayConfigMapName(instance.Name)}},
	} {
		if err := client.IgnoreNotFound(rc.Client().Delete(rc.Context(), obj)); err != nil {
			return err
		}
	}
	return nil
}

func gatewayEnabled(instance *v1.KDBInstance) bool {
	if instance == nil || instance.Spec.ClickHouse == nil || instance.Spec.ClickHouse.Gateway == nil {
		return false
	}
	return instance.Spec.ClickHouse.Gateway.Enabled != nil && *instance.Spec.ClickHouse.Gateway.Enabled
}

func desiredGatewayReplicas(instance *v1.KDBInstance) int32 {
	if instance.Spec.ClickHouse.Gateway != nil && instance.Spec.ClickHouse.Gateway.Replicas != nil {
		return *instance.Spec.ClickHouse.Gateway.Replicas
	}
	return 2
}

func buildGatewayConfigMap(instance *v1.KDBInstance, bindings gatewayBindingSpec) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Namespace: instance.Namespace,
		Name:      naming.ClickHouseGatewayConfigMapName(instance.Name),
		Labels:    naming.Merge(instance.Labels, naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentGateway)),
	}}
	cm.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
	cm.Data = map[string]string{gatewayConfigKey: naming.YamlGeneratedWarning + renderGatewayConfig(instance, bindings)}
	return cm
}

func buildGatewayDeployment(instance *v1.KDBInstance, bindingSecretName, configRevision string) *appsv1.Deployment {
	labels := naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentGateway)
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: instance.Namespace,
		Name:      naming.ClickHouseGatewayDeploymentName(instance.Name),
		Labels:    naming.Merge(instance.Labels, labels),
	}}
	deployment.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("Deployment"))
	replicas := desiredGatewayReplicas(instance)
	deployment.Spec.Replicas = &replicas
	deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
	deployment.Spec.Template.Labels = naming.Merge(instance.Labels, labels)
	deployment.Spec.Template.Annotations = map[string]string{"clickhouse.kdb.com/gateway-config-revision": configRevision}
	deployment.Spec.Template.Spec.SecurityContext = security.PodSecurityContext(instance)
	deployment.Spec.Template.Spec.EnableServiceLinks = util.Bool(false)
	deployment.Spec.Template.Spec.Affinity = &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
			LabelSelector: &metav1.LabelSelector{MatchLabels: labels},
			TopologyKey: "kubernetes.io/hostname",
		}},
	}}
	deployment.Spec.Template.Spec.Volumes = []corev1.Volume{
		{
			Name: gatewayConfigVolumeName,
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: naming.ClickHouseGatewayConfigMapName(instance.Name)},
			}},
		},
		{
			Name: "gateway-bindings",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: bindingSecretName,
			}},
		},
	}
	deployment.Spec.Template.Spec.Containers = []corev1.Container{{
		Name:            "chproxy",
		Image:           gatewayImage(instance),
		Args:            []string{"-config", "/etc/chproxy/" + gatewayConfigKey},
		SecurityContext: security.InitClickHouseSecurityContext(),
		EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: bindingSecretName},
		}}},
		Ports: []corev1.ContainerPort{
			{Name: "http", ContainerPort: naming.ClickHouseGatewayHTTPPort(), Protocol: corev1.ProtocolTCP},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: gatewayConfigVolumeName, MountPath: "/etc/chproxy", ReadOnly: true},
			{Name: "gateway-bindings", MountPath: "/etc/chproxy-secret", ReadOnly: true},
		},
		ReadinessProbe: gatewayProbe(),
		LivenessProbe:  gatewayProbe(),
	}}
	return deployment
}

func buildGatewayService(instance *v1.KDBInstance) *corev1.Service {
	labels := naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentGateway)
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Namespace: instance.Namespace,
		Name:      naming.ClickHouseGatewayServiceName(instance.Name),
		Labels:    naming.Merge(instance.Labels, labels),
	}}
	service.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
	service.Spec.Selector = labels
	if instance.Spec.ClickHouse.Gateway != nil && instance.Spec.ClickHouse.Gateway.Service != nil {
		serviceSpec := instance.Spec.ClickHouse.Gateway.Service
		if serviceSpec.Metadata != nil {
			service.Labels = naming.Merge(service.Labels, serviceSpec.Metadata.GetLabelsOrNil())
			service.Annotations = naming.Merge(service.Annotations, serviceSpec.Metadata.GetAnnotationsOrNil())
		}
		if serviceSpec.Type != "" {
			service.Spec.Type = corev1.ServiceType(serviceSpec.Type)
		}
	}
	service.Spec.Ports = []corev1.ServicePort{
		{Name: "http", Port: naming.ClickHouseGatewayHTTPPort(), TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP},
		{Name: "metrics", Port: naming.ClickHouseGatewayMetricsPort(), TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP},
	}
	if instance.Spec.ClickHouse.Gateway != nil && instance.Spec.ClickHouse.Gateway.Service != nil && instance.Spec.ClickHouse.Gateway.Service.NodePort != nil {
		service.Spec.Ports[0].NodePort = *instance.Spec.ClickHouse.Gateway.Service.NodePort
	}
	return service
}

func gatewayProbe() *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
			Path: "/metrics",
			Port: intstr.FromString("http"),
		}},
		FailureThreshold: 3,
		PeriodSeconds:    10,
		TimeoutSeconds:   3,
	}
}

func gatewayImage(instance *v1.KDBInstance) string {
	if instance.Spec.Config != nil && instance.Spec.Config["clickhouse.gateway.image"] != "" {
		return instance.Spec.Config["clickhouse.gateway.image"]
	}
	return ""
}

func setGatewayReadyCondition(instance *v1.KDBInstance, status metav1.ConditionStatus, reason, message string, readyReplicas int32) {
	if instance.Status.ClickHouse == nil {
		instance.Status.ClickHouse = &v1.ClickHouseStatus{DataShards: instance.Spec.ClickHouse.DataShards}
	}
	instance.Status.ClickHouse.Gateway = &v1.ClickHouseGatewayStatus{
		ReadyReplicas: readyReplicas,
		HTTPEndpoint:  naming.ClickHouseGatewayServiceName(instance.Name),
		NativePhase:   "Unsupported",
	}
	if gatewayEnabled(instance) && status != metav1.ConditionTrue && instance.Status.Phase == clickHousePhaseRunning {
		instance.Status.Phase = clickHousePhaseDegraded
	}
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               "GatewayReady",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: instance.Generation,
	})
}
