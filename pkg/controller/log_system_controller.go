package controller

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"time"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	KDBLogSystemControllerName = "kdb-log-system-controller"

	defaultLogSystemBackendType = "custom_http"
	defaultLogSystemImage       = "sqc157400661/kdb-log-gateway:latest"
	defaultLogSystemPortName    = "http"
	defaultLogSystemPort        = int32(3100)
)

type KDBLogSystemReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=kdb.com,resources=kdblogsystems,verbs=get;list;watch
// +kubebuilder:rbac:groups=kdb.com,resources=kdblogsystems/status,verbs=patch
func (r *KDBLogSystemReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("controllers").WithName("kdb-log-system")
	logSystem := &v1.KDBLogSystem{}
	if err := r.Get(ctx, request.NamespacedName, logSystem); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !logSystem.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if logSystem.Spec.BackendID == "" {
		return r.patchStatus(ctx, logSystem, v1.LogSystemPhaseFailed, 0, "", "spec.backendId is required", 0)
	}
	if logSystem.Spec.CredentialSecretRef != nil && logSystem.Spec.CredentialSecretRef.Name != "" {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: logSystem.Namespace, Name: logSystem.Spec.CredentialSecretRef.Name}, secret); err != nil {
			if apierrors.IsNotFound(err) {
				return r.patchStatus(ctx, logSystem, v1.LogSystemPhaseProgressing, 0, "", "waiting for credential secret", 30*time.Second)
			}
			return ctrl.Result{}, err
		}
	}

	configHash, err := r.reconcileConfigMap(ctx, logSystem)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileService(ctx, logSystem); err != nil {
		return ctrl.Result{}, err
	}
	deployment, err := r.reconcileDeployment(ctx, logSystem, configHash)
	if err != nil {
		return ctrl.Result{}, err
	}

	phase := v1.LogSystemPhaseProgressing
	message := "waiting for gateway deployment rollout"
	if isLogSystemDeploymentReady(deployment, desiredLogSystemReplicas(logSystem)) {
		phase = v1.LogSystemPhaseReady
		message = "log system gateway is ready"
	}
	logger.V(1).Info("reconciled log system", "phase", phase, "configHash", configHash)
	return r.patchStatus(ctx, logSystem, phase, deployment.Status.ReadyReplicas, configHash, message, 0)
}

func (r *KDBLogSystemReconciler) reconcileConfigMap(ctx context.Context, logSystem *v1.KDBLogSystem) (string, error) {
	name := logSystemResourceName(logSystem)
	body := map[string]any{
		"backendId":      logSystem.Spec.BackendID,
		"backendType":    firstNonEmptyLogSystem(logSystem.Spec.BackendType, defaultLogSystemBackendType),
		"loggingAppCode": logSystem.Spec.LoggingAppCode,
		"retentionDays":  logSystem.Spec.RetentionDays,
		"endpoints":      logSystem.Spec.Endpoints,
		"collector":      logSystem.Spec.Collector,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	hash := shortLogSystemHash(raw)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: logSystem.Namespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if err := controllerutil.SetControllerReference(logSystem, cm, r.Scheme); err != nil {
			return err
		}
		cm.Labels = logSystemLabels(logSystem)
		cm.Data = map[string]string{
			"backend.json": string(raw),
			"configHash":   hash,
		}
		return nil
	})
	return hash, err
}

func (r *KDBLogSystemReconciler) reconcileService(ctx context.Context, logSystem *v1.KDBLogSystem) error {
	name := logSystemResourceName(logSystem)
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: logSystem.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(logSystem, svc, r.Scheme); err != nil {
			return err
		}
		svc.Labels = logSystemLabels(logSystem)
		svc.Spec.Selector = logSystemLabels(logSystem)
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       defaultLogSystemPortName,
			Port:       defaultLogSystemPort,
			TargetPort: intstr.FromString(defaultLogSystemPortName),
		}}
		return nil
	})
	return err
}

func (r *KDBLogSystemReconciler) reconcileDeployment(ctx context.Context, logSystem *v1.KDBLogSystem, configHash string) (*appsv1.Deployment, error) {
	name := logSystemResourceName(logSystem)
	labels := logSystemLabels(logSystem)
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: logSystem.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		if err := controllerutil.SetControllerReference(logSystem, deploy, r.Scheme); err != nil {
			return err
		}
		replicas := desiredLogSystemReplicas(logSystem)
		deploy.Labels = labels
		deploy.Spec.Replicas = &replicas
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		deploy.Spec.Template.Labels = labels
		deploy.Spec.Template.Annotations = map[string]string{"kdb.com/log-config-hash": configHash}
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  "gateway",
			Image: firstNonEmptyLogSystem(logSystem.Spec.Gateway.Image, defaultLogSystemImage),
			Ports: []corev1.ContainerPort{{
				Name:          defaultLogSystemPortName,
				ContainerPort: defaultLogSystemPort,
			}},
			Env: []corev1.EnvVar{
				{Name: "KDB_LOG_BACKEND_ID", Value: logSystem.Spec.BackendID},
				{Name: "KDB_LOG_BACKEND_TYPE", Value: firstNonEmptyLogSystem(logSystem.Spec.BackendType, defaultLogSystemBackendType)},
				{Name: "KDB_LOG_CONFIG_HASH", Value: configHash},
			},
			Resources: logSystem.Spec.Gateway.Resources,
			VolumeMounts: []corev1.VolumeMount{{
				Name:      "backend-config",
				MountPath: "/etc/kdb/log-system",
				ReadOnly:  true,
			}},
		}}
		if logSystem.Spec.CredentialSecretRef != nil && logSystem.Spec.CredentialSecretRef.Name != "" {
			deploy.Spec.Template.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{
				SecretRef: &corev1.SecretEnvSource{LocalObjectReference: *logSystem.Spec.CredentialSecretRef},
			}}
		}
		deploy.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name: "backend-config",
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: name},
			}},
		}}
		return nil
	})
	return deploy, err
}

func (r *KDBLogSystemReconciler) patchStatus(ctx context.Context, logSystem *v1.KDBLogSystem, phase string, readyReplicas int32, configHash, message string, requeueAfter time.Duration) (ctrl.Result, error) {
	before := logSystem.DeepCopy()
	logSystem.Status.Phase = phase
	logSystem.Status.ObservedGeneration = logSystem.Generation
	logSystem.Status.ServiceName = logSystemResourceName(logSystem)
	logSystem.Status.ConfigHash = configHash
	logSystem.Status.ReadyReplicas = readyReplicas
	logSystem.Status.Message = message
	conditionStatus := metav1.ConditionFalse
	if phase == v1.LogSystemPhaseReady {
		conditionStatus = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&logSystem.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		ObservedGeneration: logSystem.Generation,
		Reason:             phase,
		Message:            message,
	})
	return ctrl.Result{RequeueAfter: requeueAfter}, r.Status().Patch(ctx, logSystem, client.MergeFrom(before))
}

func (r *KDBLogSystemReconciler) SetupWithManager(mgr manager.Manager) error {
	return builder.ControllerManagedBy(mgr).
		For(&v1.KDBLogSystem{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Complete(r)
}

func desiredLogSystemReplicas(logSystem *v1.KDBLogSystem) int32 {
	if logSystem.Spec.Gateway.Replicas != nil && *logSystem.Spec.Gateway.Replicas > 0 {
		return *logSystem.Spec.Gateway.Replicas
	}
	return 1
}

func isLogSystemDeploymentReady(deployment *appsv1.Deployment, desired int32) bool {
	return deployment.Status.ObservedGeneration >= deployment.Generation &&
		deployment.Status.UpdatedReplicas == desired &&
		deployment.Status.ReadyReplicas == desired &&
		deployment.Status.AvailableReplicas == desired
}

func logSystemResourceName(logSystem *v1.KDBLogSystem) string {
	if logSystem.Name != "" {
		return logSystem.Name
	}
	return fmt.Sprintf("kdb-log-%s", logSystem.Spec.BackendID)
}

func logSystemLabels(logSystem *v1.KDBLogSystem) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "kdb-log-system",
		"app.kubernetes.io/managed-by": "kdb-operator",
		"kdb.com/log-backend-hash":     shortLogSystemHash([]byte(logSystem.Spec.BackendID)),
	}
}

func shortLogSystemHash(raw []byte) string {
	sum := sha1.Sum(raw)
	return hex.EncodeToString(sum[:])[:12]
}

func firstNonEmptyLogSystem(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
