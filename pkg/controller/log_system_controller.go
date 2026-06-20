package controller

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
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
// +kubebuilder:rbac:groups=apps,resources=daemonsets;deployments,verbs=create;delete;get;list;patch;update;watch
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
	daemonSet, err := r.reconcileCollectorDaemonSet(ctx, logSystem, configHash)
	if err != nil {
		return ctrl.Result{}, err
	}

	phase := v1.LogSystemPhaseProgressing
	message := "waiting for gateway deployment rollout"
	if isLogSystemDeploymentReady(deployment, desiredLogSystemReplicas(logSystem)) {
		message = "waiting for collector daemonset rollout"
	}
	if isLogSystemDeploymentReady(deployment, desiredLogSystemReplicas(logSystem)) && isCollectorDaemonSetReady(daemonSet) {
		phase = v1.LogSystemPhaseReady
		message = "log system gateway and collector are ready"
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
			"backend.json":    string(raw),
			"configHash":      hash,
			"fluent-bit.conf": renderLogSystemFluentBitConfig(logSystem.Spec.Endpoints.Write),
			"parsers.conf":    renderLogSystemFluentBitParsers(),
		}
		return nil
	})
	return hash, err
}

func (r *KDBLogSystemReconciler) reconcileService(ctx context.Context, logSystem *v1.KDBLogSystem) error {
	name := logSystemResourceName(logSystem)
	selectorLabels := logSystemSelectorLabels(logSystem)
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: logSystem.Namespace, Name: name}, deploy); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	} else if deploy.Spec.Selector != nil && len(deploy.Spec.Selector.MatchLabels) > 0 {
		selectorLabels = mergeLogSystemLabels(nil, deploy.Spec.Selector.MatchLabels)
	}

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: logSystem.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(logSystem, svc, r.Scheme); err != nil {
			return err
		}
		svc.Labels = logSystemLabels(logSystem)
		svc.Spec.Selector = selectorLabels
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
	selectorLabels := logSystemSelectorLabels(logSystem)
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: logSystem.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		if err := controllerutil.SetControllerReference(logSystem, deploy, r.Scheme); err != nil {
			return err
		}
		replicas := desiredLogSystemReplicas(logSystem)
		deploy.Labels = labels
		deploy.Spec.Replicas = &replicas
		if deploy.Spec.Selector == nil {
			deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: selectorLabels}
		}
		deploy.Spec.Template.Labels = mergeLogSystemLabels(labels, deploy.Spec.Selector.MatchLabels)
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

func (r *KDBLogSystemReconciler) reconcileCollectorDaemonSet(ctx context.Context, logSystem *v1.KDBLogSystem, configHash string) (*appsv1.DaemonSet, error) {
	name := firstNonEmptyLogSystem(logSystem.Spec.Collector.DaemonSet, logSystemResourceName(logSystem)+"-collector")
	namespace := firstNonEmptyLogSystem(logSystem.Spec.Collector.Namespace, logSystem.Namespace)
	labels := mergeLogSystemLabels(logSystemLabels(logSystem), map[string]string{
		"app.kubernetes.io/component": "log-collector",
	})
	selectorLabels := map[string]string{
		"app.kubernetes.io/name":      "kdb-log-collector",
		"app.kubernetes.io/instance":  name,
		"kdb.com/log-backend-hash":    shortLogSystemHash([]byte(logSystem.Spec.BackendID)),
		"kdb.com/log-backend-owner":   logSystem.Name,
		"kdb.com/log-backend-ns":      logSystem.Namespace,
		"kdb.com/log-backend-id-safe": shortLogSystemHash([]byte(logSystem.Spec.BackendID + ":collector")),
	}
	ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ds, func() error {
		if namespace == logSystem.Namespace {
			if err := controllerutil.SetControllerReference(logSystem, ds, r.Scheme); err != nil {
				return err
			}
		}
		ds.Labels = labels
		if ds.Spec.Selector == nil {
			ds.Spec.Selector = &metav1.LabelSelector{MatchLabels: selectorLabels}
		}
		templateLabels := mergeLogSystemLabels(labels, ds.Spec.Selector.MatchLabels)
		ds.Spec.Template.Labels = templateLabels
		ds.Spec.Template.Annotations = map[string]string{"kdb.com/log-config-hash": configHash}
		ds.Spec.Template.Spec.ServiceAccountName = "kdb"
		ds.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  "fluent-bit",
			Image: firstNonEmptyLogSystem(logSystem.Spec.Collector.Image, "fluent/fluent-bit:3.0"),
			Env: []corev1.EnvVar{
				{Name: "KDB_LOG_BACKEND_ID", Value: logSystem.Spec.BackendID},
				{Name: "KDB_LOG_BACKEND_TYPE", Value: firstNonEmptyLogSystem(logSystem.Spec.BackendType, defaultLogSystemBackendType)},
				{Name: "KDB_LOG_CONFIG_HASH", Value: configHash},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "backend-config", MountPath: "/etc/kdb/log-system", ReadOnly: true},
				{Name: "backend-config", MountPath: "/fluent-bit/etc", ReadOnly: true},
				{Name: "varlog", MountPath: "/var/log", ReadOnly: true},
				{Name: "kubelet-pods", MountPath: "/var/lib/kubelet/pods", ReadOnly: true},
			},
		}}
		ds.Spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: "backend-config",
				VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: logSystemResourceName(logSystem)},
				}},
			},
			{
				Name:         "varlog",
				VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/log"}},
			},
			{
				Name:         "kubelet-pods",
				VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/pods"}},
			},
		}
		return nil
	})
	return ds, err
}

func renderLogSystemFluentBitConfig(writeEndpoint string) string {
	return fmt.Sprintf(`[SERVICE]
    Flush        5
    Daemon       Off
    Log_Level    info
    Parsers_File parsers.conf

[INPUT]
    Name              tail
    Path              /var/log/containers/*.log
    Parser            cri
    Tag               kube.*
    Path_Key          file_path
    Refresh_Interval  5
    Mem_Buf_Limit     50MB
    Skip_Long_Lines   On

[INPUT]
    Name              tail
    Path              /var/lib/kubelet/pods/*/volumes/*/*/log/*.log,/var/lib/kubelet/pods/*/volumes/*/*/logs/*.log
    Parser            mysql_file
    Tag               kdb.mysql.file
    Path_Key          file_path
    Refresh_Interval  5
    Mem_Buf_Limit     50MB
    Skip_Long_Lines   On

[FILTER]
    Name                kubernetes
    Match               kube.*
    Merge_Log           On
    Keep_Log            Off
    K8S-Logging.Parser  On
    K8S-Logging.Exclude Off

[OUTPUT]
    Name        loki
    Match       *
%s
    Labels      job=kdb,source=fluent-bit
    Label_Keys  $kubernetes['namespace_name'],$kubernetes['pod_name'],$kubernetes['container_name'],$kubernetes['host'],$file_path
    Line_Format json
`, renderLogSystemLokiOutputEndpoint(writeEndpoint))
}

func renderLogSystemLokiOutputEndpoint(writeEndpoint string) string {
	parsed, err := url.Parse(writeEndpoint)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Sprintf("    URI         %s", writeEndpoint)
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	uri := parsed.EscapedPath()
	if uri == "" {
		uri = "/loki/api/v1/push"
	}
	if parsed.RawQuery != "" {
		uri += "?" + parsed.RawQuery
	}
	tls := ""
	if parsed.Scheme == "https" {
		tls = "\n    TLS         On"
	}
	return fmt.Sprintf("    Host        %s\n    Port        %s\n    URI         %s%s", parsed.Hostname(), port, uri, tls)
}

func renderLogSystemFluentBitParsers() string {
	return `[PARSER]
    Name        cri
    Format      regex
    Regex       ^(?<time>[^ ]+) (?<stream>stdout|stderr) (?<logtag>[^ ]*) (?<log>.*)$
    Time_Key    time
    Time_Format %Y-%m-%dT%H:%M:%S.%L%z

[PARSER]
    Name        mysql_file
    Format      regex
    Regex       ^(?<log>.*)$
`
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
		Owns(&appsv1.DaemonSet{}).
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

func isCollectorDaemonSetReady(daemonSet *appsv1.DaemonSet) bool {
	if daemonSet == nil {
		return false
	}
	if daemonSet.Status.DesiredNumberScheduled == 0 {
		return daemonSet.Status.ObservedGeneration >= daemonSet.Generation
	}
	return daemonSet.Status.ObservedGeneration >= daemonSet.Generation &&
		daemonSet.Status.UpdatedNumberScheduled == daemonSet.Status.DesiredNumberScheduled &&
		daemonSet.Status.NumberReady == daemonSet.Status.DesiredNumberScheduled &&
		daemonSet.Status.NumberAvailable == daemonSet.Status.DesiredNumberScheduled
}

func logSystemResourceName(logSystem *v1.KDBLogSystem) string {
	if logSystem.Name != "" {
		return logSystem.Name
	}
	return fmt.Sprintf("kdb-log-%s", logSystem.Spec.BackendID)
}

func logSystemSelectorLabels(logSystem *v1.KDBLogSystem) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "kdb-log-system",
		"app.kubernetes.io/managed-by": "kdb-operator",
		"app.kubernetes.io/instance":   logSystemResourceName(logSystem),
	}
}

func logSystemLabels(logSystem *v1.KDBLogSystem) map[string]string {
	labels := logSystemSelectorLabels(logSystem)
	labels["kdb.com/log-backend-hash"] = shortLogSystemHash([]byte(logSystem.Spec.BackendID))
	return labels
}

func mergeLogSystemLabels(base map[string]string, overrides map[string]string) map[string]string {
	labels := make(map[string]string, len(base)+len(overrides))
	for key, value := range base {
		labels[key] = value
	}
	for key, value := range overrides {
		labels[key] = value
	}
	return labels
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
