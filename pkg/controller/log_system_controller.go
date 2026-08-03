package controller

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
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
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=create;get;list;patch;update;watch
// +kubebuilder:rbac:groups=apps,resources=daemonsets;deployments,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=create;get;list;patch;update;watch
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
	if err := r.reconcileCollectorRBAC(ctx, logSystem); err != nil {
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
	podScopes, err := r.collectLogPodScopes(ctx, logSystemCollectorNamespace(logSystem))
	if err != nil {
		return "", err
	}
	fluentBitConfig := renderLogSystemFluentBitConfig(logSystem.Spec.Endpoints.Write, logSystem.Spec.Collector.ExtraLogDirs)
	parsersConfig := renderLogSystemFluentBitParsers()
	podScopeLua := renderLogSystemPodScopeLua(podScopes)
	hashBody := append(append(append(append([]byte{}, raw...), fluentBitConfig...), parsersConfig...), podScopeLua...)
	hash := shortLogSystemHash(hashBody)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: logSystem.Namespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if err := controllerutil.SetControllerReference(logSystem, cm, r.Scheme); err != nil {
			return err
		}
		cm.Labels = logSystemLabels(logSystem)
		cm.Data = map[string]string{
			"backend.json":    string(raw),
			"configHash":      hash,
			"fluent-bit.conf": fluentBitConfig,
			"parsers.conf":    parsersConfig,
			"pod_scope.lua":   podScopeLua,
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

func (r *KDBLogSystemReconciler) reconcileCollectorRBAC(ctx context.Context, logSystem *v1.KDBLogSystem) error {
	name := logSystemCollectorName(logSystem)
	namespace := logSystemCollectorNamespace(logSystem)
	labels := mergeLogSystemLabels(logSystemLabels(logSystem), map[string]string{
		"app.kubernetes.io/component": "log-collector",
	})

	account := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, account, func() error {
		if namespace == logSystem.Namespace {
			if err := controllerutil.SetControllerReference(logSystem, account, r.Scheme); err != nil {
				return err
			}
		}
		account.Labels = labels
		return nil
	}); err != nil {
		return err
	}

	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Labels = labels
		role.Rules = []rbacv1.PolicyRule{{
			APIGroups: []string{""},
			Resources: []string{"pods", "namespaces", "nodes"},
			Verbs:     []string{"get", "list", "watch"},
		}}
		return nil
	}); err != nil {
		return err
	}

	binding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
		binding.Labels = labels
		binding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     name,
		}
		binding.Subjects = []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      name,
			Namespace: namespace,
		}}
		return nil
	})
	return err
}

func (r *KDBLogSystemReconciler) reconcileCollectorDaemonSet(ctx context.Context, logSystem *v1.KDBLogSystem, configHash string) (*appsv1.DaemonSet, error) {
	name := logSystemCollectorName(logSystem)
	namespace := logSystemCollectorNamespace(logSystem)
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
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
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
			ds.Spec.Template.Spec.ServiceAccountName = name
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
					{Name: "local-path-pvs", MountPath: "/var/local-path-provisioner", ReadOnly: true},
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
				{
					Name: "local-path-pvs",
					VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
						Path: "/var/local-path-provisioner",
						Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
					}},
				},
			}
			return nil
		})
		return err
	})
	return ds, err
}

func renderLogSystemFluentBitConfig(writeEndpoint string, extraLogDirs []string) string {
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
    Path              %s
    Parser            database_file
    Tag               kdb.database.file
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

[FILTER]
    Name                lua
    Match               *
    Script              pod_scope.lua
    Call                enrich_pod_scope

[OUTPUT]
    Name        loki
    Match       *
%s
    Labels      job=kdb,source=fluent-bit
    Label_Keys  $kubernetes['namespace_name'],$kubernetes['pod_name'],$kubernetes['container_name'],$kubernetes['host'],$pod_uid,$pod_name,$pod_namespace,$file_path
    Line_Format json
`, strings.Join(renderLogSystemFileLogPaths(extraLogDirs), ","), renderLogSystemLokiOutputEndpoint(writeEndpoint))
}

func renderLogSystemFileLogPaths(extraLogDirs []string) []string {
	dirs := []string{"/kdbdata/log"}
	dirs = append(dirs, extraLogDirs...)
	seenDirs := map[string]struct{}{}
	seenPaths := map[string]struct{}{}
	paths := make([]string, 0, len(dirs)*2)
	for _, dir := range dirs {
		dir = normalizeLogSystemContainerLogDir(dir)
		if dir == "" {
			continue
		}
		if _, ok := seenDirs[dir]; ok {
			continue
		}
		seenDirs[dir] = struct{}{}
		if dir == "/kdbdata/log" {
			// /kdbdata is the data PVC's container mount point. At the CSI
			// volume root the same directory is mount/log/.
			for _, path := range []string{
				"/var/lib/kubelet/pods/*/volumes/*/*/mount/log/*.log",
				"/var/local-path-provisioner/*/log/*.log",
			} {
				if _, ok := seenPaths[path]; !ok {
					seenPaths[path] = struct{}{}
					paths = append(paths, path)
				}
			}
			continue
		}
		for _, prefix := range []string{
			"/var/lib/kubelet/pods/*/volumes/*/*",
			"/var/lib/kubelet/pods/*/volumes/*/*/mount",
		} {
			path := prefix + dir + "/*.log"
			if _, ok := seenPaths[path]; ok {
				continue
			}
			seenPaths[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	return paths
}

type logPodScope struct {
	PodUID    string
	PodName   string
	Namespace string
}

func (r *KDBLogSystemReconciler) collectLogPodScopes(ctx context.Context, namespace string) (map[string]logPodScope, error) {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	scopes := map[string]logPodScope{}
	ambiguous := map[string]struct{}{}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !pod.DeletionTimestamp.IsZero() || pod.UID == "" || strings.TrimSpace(pod.Labels["kdb.instance"]) == "" || strings.EqualFold(pod.Labels["kdb.com/component"], "keeper") {
			continue
		}
		scope := logPodScope{PodUID: string(pod.UID), PodName: pod.Name, Namespace: pod.Namespace}
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim == nil || strings.TrimSpace(volume.PersistentVolumeClaim.ClaimName) == "" {
				continue
			}
			claim := strings.TrimSpace(volume.PersistentVolumeClaim.ClaimName)
			if existing, ok := scopes[claim]; ok && existing.PodUID != scope.PodUID {
				delete(scopes, claim)
				ambiguous[claim] = struct{}{}
				continue
			}
			if _, conflict := ambiguous[claim]; !conflict {
				scopes[claim] = scope
			}
		}
	}
	return scopes, nil
}

func renderLogSystemPodScopeLua(scopes map[string]logPodScope) string {
	claims := make([]string, 0, len(scopes))
	for claim := range scopes {
		claims = append(claims, claim)
	}
	sort.Strings(claims)
	var builder strings.Builder
	builder.WriteString("local pod_scopes = {\n")
	for _, claim := range claims {
		scope := scopes[claim]
		fmt.Fprintf(&builder, "  [%s] = { pod_uid = %s, pod_name = %s, pod_namespace = %s },\n",
			strconv.Quote(claim), strconv.Quote(scope.PodUID), strconv.Quote(scope.PodName), strconv.Quote(scope.Namespace))
	}
	builder.WriteString(`}

local function set_if_empty(record, key, value)
  if value ~= nil and value ~= "" and (record[key] == nil or record[key] == "") then
    record[key] = value
  end
end

function enrich_pod_scope(tag, timestamp, record)
  local kubernetes = record["kubernetes"]
  if type(kubernetes) == "table" then
    set_if_empty(record, "pod_uid", kubernetes["pod_id"] or kubernetes["pod_uid"])
    set_if_empty(record, "pod_name", kubernetes["pod_name"])
    set_if_empty(record, "pod_namespace", kubernetes["namespace_name"])
  end

  local file_path = record["file_path"]
  if type(file_path) == "string" then
    set_if_empty(record, "pod_uid", string.match(file_path, "/pods/([^/]+)/volumes/"))
    local claim = string.match(file_path, "/var/local%-path%-provisioner/[^/]+_[^_]+_([^/]+)/log/[^/]+%.log$")
    local scope = claim and pod_scopes[claim] or nil
    if scope ~= nil then
      set_if_empty(record, "pod_uid", scope.pod_uid)
      set_if_empty(record, "pod_name", scope.pod_name)
      set_if_empty(record, "pod_namespace", scope.pod_namespace)
    end
  end
  return 1, timestamp, record
end
`)
	return builder.String()
}

func hostPathTypePtr(value corev1.HostPathType) *corev1.HostPathType {
	return &value
}

func normalizeLogSystemContainerLogDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || !strings.HasPrefix(dir, "/") || strings.ContainsAny(dir, "*?[") {
		return ""
	}
	dir = strings.TrimRight(dir, "/")
	if dir == "" || strings.HasSuffix(dir, ".log") {
		return ""
	}
	return dir
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
    Name        database_file
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
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Service{}).
		Watches(&source.Kind{Type: &corev1.Pod{}}, handler.EnqueueRequestsFromMapFunc(logSystemRequestsForDatabasePod(mgr.GetClient()))).
		Complete(r)
}

func logSystemRequestsForDatabasePod(cache client.Client) handler.MapFunc {
	return func(object client.Object) []reconcile.Request {
		if cache == nil || object == nil || strings.TrimSpace(object.GetLabels()["kdb.instance"]) == "" {
			return nil
		}
		logSystems := &v1.KDBLogSystemList{}
		if err := cache.List(context.Background(), logSystems); err != nil {
			return nil
		}
		requests := make([]reconcile.Request, 0, len(logSystems.Items))
		for i := range logSystems.Items {
			logSystem := &logSystems.Items[i]
			if logSystemCollectorNamespace(logSystem) != object.GetNamespace() {
				continue
			}
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: logSystem.Namespace, Name: logSystem.Name}})
		}
		return requests
	}
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

func logSystemCollectorName(logSystem *v1.KDBLogSystem) string {
	return firstNonEmptyLogSystem(logSystem.Spec.Collector.DaemonSet, logSystemResourceName(logSystem)+"-collector")
}

func logSystemCollectorNamespace(logSystem *v1.KDBLogSystem) string {
	return firstNonEmptyLogSystem(logSystem.Spec.Collector.Namespace, logSystem.Namespace)
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
