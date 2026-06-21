package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	KDBMonitoringStackControllerName = "kdb-monitoring-stack-controller"

	defaultMonitoringStackNamespace = "kdb-observability"
	defaultMonitoringStackName      = "kdb"
	defaultMonitoringOperatorVer    = "v0.92.0"
	defaultGrafanaImage             = "grafana/grafana:12.0.1"
	prometheusOperatorDeployment    = "prometheus-operator"
	prometheusService               = "kdb-prometheus"
	alertmanagerService             = "kdb-alertmanager"
	grafanaDeployment               = "kdb-grafana"
)

type KDBMonitoringStackReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Config     *rest.Config
	Recorder   record.EventRecorder
	HTTPClient *http.Client
}

// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=create;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=configmaps;pods;serviceaccounts;services,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=create;get;list;patch;update;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings;roles;rolebindings,verbs=create;get;list;patch;update;watch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=alertmanagers;alertmanagerconfigs;podmonitors;probes;prometheuses;prometheusagents;prometheusrules;scrapeconfigs;servicemonitors;thanosrulers,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbmonitoringstacks,verbs=get;list;watch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbmonitoringstacks/status,verbs=patch
func (r *KDBMonitoringStackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("controllers").WithName("kdb-monitoring-stack")
	stack := &v1.KDBMonitoringStack{}
	if err := r.Get(ctx, request.NamespacedName, stack); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !stack.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	dynamicClient, err := dynamic.NewForConfig(r.Config)
	if err != nil {
		return r.patchStatus(ctx, stack, v1.MonitoringStackPhaseFailed, false, nil, "", err.Error(), 0)
	}
	clientset, err := kubernetes.NewForConfig(r.Config)
	if err != nil {
		return r.patchStatus(ctx, stack, v1.MonitoringStackPhaseFailed, false, nil, "", err.Error(), 0)
	}

	namespace := monitoringStackNamespace(stack)
	bundle, err := r.loadPrometheusOperatorBundle(ctx, stack)
	if err != nil {
		return r.patchStatus(ctx, stack, v1.MonitoringStackPhaseFailed, false, nil, "", err.Error(), time.Minute)
	}
	rewriteMonitoringBundle(bundle, namespace)
	manifests := append([]map[string]any{monitoringNamespaceManifest(namespace)}, bundle...)
	manifests = append(manifests, monitoringStackManifests(stack, namespace)...)
	for _, manifest := range manifests {
		if err := applyMonitoringManifest(ctx, dynamicClient, manifest); err != nil {
			logger.Error(err, "apply monitoring manifest failed", "kind", manifest["kind"])
			return r.patchStatus(ctx, stack, v1.MonitoringStackPhaseProgressing, false, nil, "", err.Error(), 15*time.Second)
		}
	}

	components := monitoringStackComponentStatuses(ctx, dynamicClient, clientset, namespace, monitoringOperatorVersion(stack))
	ready := monitoringStackReady(components)
	phase := v1.MonitoringStackPhaseProgressing
	message := "waiting for monitoring stack rollout"
	if ready {
		phase = v1.MonitoringStackPhaseReady
		message = "monitoring stack is ready"
	}
	return r.patchStatus(ctx, stack, phase, ready, components, fmt.Sprintf("k8s-proxy://%s/%s:9090", namespace, prometheusService), message, requeueWhenNotReady(ready))
}

func (r *KDBMonitoringStackReconciler) loadPrometheusOperatorBundle(ctx context.Context, stack *v1.KDBMonitoringStack) ([]map[string]any, error) {
	client := r.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, monitoringBundleURL(stack), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download prometheus operator bundle returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return splitMonitoringYAML(data)
}

func (r *KDBMonitoringStackReconciler) patchStatus(ctx context.Context, stack *v1.KDBMonitoringStack, phase string, ready bool, components []v1.MonitoringStackComponentStatus, prometheusURL, message string, requeueAfter time.Duration) (ctrl.Result, error) {
	base := stack.DeepCopy()
	stack.Status.Phase = phase
	stack.Status.ObservedGeneration = stack.Generation
	stack.Status.Ready = ready
	stack.Status.Components = components
	stack.Status.PrometheusURL = prometheusURL
	stack.Status.Message = message
	if err := r.Status().Patch(ctx, stack, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *KDBMonitoringStackReconciler) SetupWithManager(mgr manager.Manager) error {
	return builder.ControllerManagedBy(mgr).
		For(&v1.KDBMonitoringStack{}).
		Complete(r)
}

func splitMonitoringYAML(data []byte) ([]map[string]any, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	out := []map[string]any{}
	for {
		obj := map[string]any{}
		err := decoder.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(obj) == 0 || obj["kind"] == nil {
			continue
		}
		out = append(out, obj)
	}
	return out, nil
}

func rewriteMonitoringBundle(manifests []map[string]any, namespace string) {
	for _, manifest := range manifests {
		obj := &unstructured.Unstructured{Object: manifest}
		if monitoringKindNamespaced(obj.GetKind()) {
			obj.SetNamespace(namespace)
		}
		if obj.GetKind() == "ClusterRoleBinding" {
			subjects, ok, _ := unstructured.NestedSlice(obj.Object, "subjects")
			if !ok {
				continue
			}
			for i := range subjects {
				subject, ok := subjects[i].(map[string]any)
				if ok && subject["kind"] == "ServiceAccount" && subject["name"] == "prometheus-operator" {
					subject["namespace"] = namespace
				}
			}
			_ = unstructured.SetNestedSlice(obj.Object, subjects, "subjects")
		}
	}
}

func applyMonitoringManifest(ctx context.Context, dynamicClient dynamic.Interface, manifest map[string]any) error {
	obj := &unstructured.Unstructured{Object: manifest}
	gvr, err := monitoringGVRForKind(obj.GetKind())
	if err != nil {
		return err
	}
	namespaceable := dynamicClient.Resource(gvr)
	var resource dynamic.ResourceInterface
	if monitoringKindNamespaced(obj.GetKind()) {
		resource = namespaceable.Namespace(obj.GetNamespace())
	} else {
		resource = namespaceable
	}
	existing, err := resource.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = resource.Create(ctx, obj, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if obj.GetKind() == "Namespace" {
		existing.SetLabels(mergeMonitoringStringMap(existing.GetLabels(), obj.GetLabels()))
		existing.SetAnnotations(mergeMonitoringStringMap(existing.GetAnnotations(), obj.GetAnnotations()))
		_, err = resource.Update(ctx, existing, metav1.UpdateOptions{})
		return err
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	_, err = resource.Update(ctx, obj, metav1.UpdateOptions{})
	return err
}

func monitoringNamespaceManifest(namespace string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/name":       "kdb-observability",
				"app.kubernetes.io/managed-by": "kdb-operator",
			},
		},
	}
}

func monitoringStackManifests(stack *v1.KDBMonitoringStack, namespace string) []map[string]any {
	selectorLabel := map[string]string{"kdb.io/monitoring": "enabled"}
	promReplicas := int64(1)
	if stack.Spec.Prometheus.Replicas != nil {
		promReplicas = int64(*stack.Spec.Prometheus.Replicas)
	}
	alertReplicas := int64(1)
	if stack.Spec.Alertmanager.Replicas != nil {
		alertReplicas = int64(*stack.Spec.Alertmanager.Replicas)
	}
	grafanaEnabled := true
	if stack.Spec.Grafana.Enabled != nil {
		grafanaEnabled = *stack.Spec.Grafana.Enabled
	}
	grafanaImage := firstNonEmptyMonitoring(stack.Spec.Grafana.Image, defaultGrafanaImage)
	retention := firstNonEmptyMonitoring(stack.Spec.Prometheus.Retention, "15d")
	manifests := []map[string]any{
		monitoringNamespaceManifest(namespace),
		{
			"apiVersion": "v1",
			"kind":       "ServiceAccount",
			"metadata": map[string]any{
				"name":      "kdb-prometheus",
				"namespace": namespace,
				"labels":    monitoringStackLabels("prometheus"),
			},
		},
		{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRole",
			"metadata": map[string]any{
				"name":   "kdb-prometheus",
				"labels": monitoringStackLabels("prometheus"),
			},
			"rules": []map[string]any{
				{"apiGroups": []string{""}, "resources": []string{"nodes", "nodes/metrics", "services", "endpoints", "pods", "namespaces", "configmaps"}, "verbs": []string{"get", "list", "watch"}},
				{"apiGroups": []string{"networking.k8s.io"}, "resources": []string{"ingresses"}, "verbs": []string{"get", "list", "watch"}},
				{"nonResourceURLs": []string{"/metrics"}, "verbs": []string{"get"}},
			},
		},
		{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRoleBinding",
			"metadata": map[string]any{
				"name":   "kdb-prometheus",
				"labels": monitoringStackLabels("prometheus"),
			},
			"roleRef": map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "kdb-prometheus"},
			"subjects": []map[string]any{{
				"kind": "ServiceAccount", "name": "kdb-prometheus", "namespace": namespace,
			}},
		},
		{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "Prometheus",
			"metadata": map[string]any{
				"name":      defaultMonitoringStackName,
				"namespace": namespace,
				"labels":    monitoringStackLabels("prometheus"),
			},
			"spec": map[string]any{
				"replicas":                        promReplicas,
				"serviceAccountName":              "kdb-prometheus",
				"retention":                       retention,
				"resources":                       monitoringResourceRequirements(stack.Spec.Prometheus.Resources, map[string]string{"cpu": "100m", "memory": "256Mi"}, map[string]string{"cpu": "1", "memory": "1Gi"}),
				"serviceMonitorSelector":          map[string]any{"matchLabels": selectorLabel},
				"serviceMonitorNamespaceSelector": map[string]any{},
				"podMonitorSelector":              map[string]any{"matchLabels": selectorLabel},
				"podMonitorNamespaceSelector":     map[string]any{},
				"probeSelector":                   map[string]any{"matchLabels": selectorLabel},
				"probeNamespaceSelector":          map[string]any{},
				"scrapeConfigSelector":            map[string]any{"matchLabels": selectorLabel},
				"scrapeConfigNamespaceSelector":   map[string]any{},
				"ruleSelector":                    map[string]any{"matchLabels": selectorLabel},
				"ruleNamespaceSelector":           map[string]any{},
				"alerting": map[string]any{
					"alertmanagers": []map[string]any{{"namespace": namespace, "name": alertmanagerService, "port": "web"}},
				},
			},
		},
		{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "Alertmanager",
			"metadata": map[string]any{
				"name":      defaultMonitoringStackName,
				"namespace": namespace,
				"labels":    monitoringStackLabels("alertmanager"),
			},
			"spec": map[string]any{
				"replicas":  alertReplicas,
				"resources": monitoringResourceRequirements(stack.Spec.Alertmanager.Resources, map[string]string{"cpu": "50m", "memory": "64Mi"}, map[string]string{"cpu": "500m", "memory": "256Mi"}),
			},
		},
		monitoringServiceManifest(namespace, prometheusService, "prometheus", map[string]string{"prometheus": defaultMonitoringStackName}, 9090),
		monitoringServiceManifest(namespace, alertmanagerService, "alertmanager", map[string]string{"alertmanager": defaultMonitoringStackName}, 9093),
	}
	if grafanaEnabled {
		manifests = append(manifests, grafanaManifests(namespace, grafanaImage, stack.Spec.Grafana.Resources)...)
	}
	return manifests
}

func monitoringServiceManifest(namespace, name, component string, selector map[string]string, port int64) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    monitoringStackLabels(component),
		},
		"spec": map[string]any{
			"selector": selector,
			"ports": []map[string]any{{
				"name":       "web",
				"port":       port,
				"targetPort": "web",
			}},
		},
	}
}

func grafanaManifests(namespace, image string, resources corev1.ResourceRequirements) []map[string]any {
	return []map[string]any{
		{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      "kdb-grafana-datasources",
				"namespace": namespace,
				"labels":    monitoringStackLabels("grafana"),
			},
			"data": map[string]string{
				"prometheus.yaml": fmt.Sprintf("apiVersion: 1\n\ndatasources:\n  - name: KDB Prometheus\n    type: prometheus\n    access: proxy\n    url: http://%s.%s.svc:9090\n    isDefault: true\n", prometheusService, namespace),
			},
		},
		{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":      grafanaDeployment,
				"namespace": namespace,
				"labels":    monitoringStackLabels("grafana"),
			},
			"spec": map[string]any{
				"replicas": int64(1),
				"selector": map[string]any{"matchLabels": map[string]string{"app.kubernetes.io/name": "kdb-grafana"}},
				"template": map[string]any{
					"metadata": map[string]any{"labels": map[string]string{"app.kubernetes.io/name": "kdb-grafana"}},
					"spec": map[string]any{
						"containers": []map[string]any{{
							"name":      "grafana",
							"image":     image,
							"resources": monitoringResourceRequirements(resources, map[string]string{"cpu": "50m", "memory": "128Mi"}, map[string]string{"cpu": "500m", "memory": "512Mi"}),
							"ports":     []map[string]any{{"name": "web", "containerPort": int64(3000)}},
							"env": []map[string]string{
								{"name": "GF_AUTH_ANONYMOUS_ENABLED", "value": "true"},
								{"name": "GF_AUTH_ANONYMOUS_ORG_ROLE", "value": "Viewer"},
								{"name": "GF_SECURITY_ADMIN_USER", "value": "admin"},
								{"name": "GF_SECURITY_ADMIN_PASSWORD", "value": "kdb-admin"},
							},
							"volumeMounts": []map[string]string{{"name": "datasources", "mountPath": "/etc/grafana/provisioning/datasources"}},
						}},
						"volumes": []map[string]any{{"name": "datasources", "configMap": map[string]string{"name": "kdb-grafana-datasources"}}},
					},
				},
			},
		},
		monitoringServiceManifest(namespace, "kdb-grafana", "grafana", map[string]string{"app.kubernetes.io/name": "kdb-grafana"}, 3000),
	}
}

func monitoringStackComponentStatuses(ctx context.Context, dynamicClient dynamic.Interface, clientset kubernetes.Interface, namespace, version string) []v1.MonitoringStackComponentStatus {
	requiredCRDs := []string{
		"prometheuses.monitoring.coreos.com",
		"prometheusagents.monitoring.coreos.com",
		"alertmanagers.monitoring.coreos.com",
		"thanosrulers.monitoring.coreos.com",
		"servicemonitors.monitoring.coreos.com",
		"podmonitors.monitoring.coreos.com",
		"probes.monitoring.coreos.com",
		"scrapeconfigs.monitoring.coreos.com",
		"prometheusrules.monitoring.coreos.com",
		"alertmanagerconfigs.monitoring.coreos.com",
	}
	crdGVR, _ := monitoringGVRForKind("CustomResourceDefinition")
	out := make([]v1.MonitoringStackComponentStatus, 0, len(requiredCRDs)+4)
	for _, name := range requiredCRDs {
		component := v1.MonitoringStackComponentStatus{Name: name, Kind: "CustomResourceDefinition", Version: version}
		_, err := dynamicClient.Resource(crdGVR).Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			component.Status = "Missing"
			component.Message = "CRD not installed"
		case err != nil:
			component.Status = "Unknown"
			component.Message = err.Error()
		default:
			component.Status = "Ready"
			component.Ready = true
		}
		out = append(out, component)
	}
	operator := monitoringDeploymentStatus(ctx, clientset, namespace, prometheusOperatorDeployment, "Prometheus Operator")
	operator.Version = version
	out = append(out,
		operator,
		monitoringPodSelectorStatus(ctx, clientset, namespace, "prometheus=kdb", "Prometheus", "Prometheus"),
		monitoringPodSelectorStatus(ctx, clientset, namespace, "alertmanager=kdb", "Alertmanager", "Alertmanager"),
		monitoringDeploymentStatus(ctx, clientset, namespace, grafanaDeployment, "Grafana"),
	)
	return out
}

func monitoringDeploymentStatus(ctx context.Context, clientset kubernetes.Interface, namespace, name, displayName string) v1.MonitoringStackComponentStatus {
	out := v1.MonitoringStackComponentStatus{Name: displayName, Kind: "Deployment", Namespace: namespace}
	deploy, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		out.Status = "Missing"
		out.Message = "Deployment not created"
	case err != nil:
		out.Status = "Unknown"
		out.Message = err.Error()
	default:
		out.Status = monitoringDeploymentRolloutStatus(deploy)
		out.Ready = deploy.Status.ReadyReplicas > 0 && deploy.Status.ReadyReplicas == deploy.Status.Replicas
		out.Message = fmt.Sprintf("%d/%d ready", deploy.Status.ReadyReplicas, deploy.Status.Replicas)
	}
	return out
}

func monitoringPodSelectorStatus(ctx context.Context, clientset kubernetes.Interface, namespace, selector, name, kind string) v1.MonitoringStackComponentStatus {
	out := v1.MonitoringStackComponentStatus{Name: name, Kind: kind, Namespace: namespace}
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		out.Status = "Unknown"
		out.Message = err.Error()
		return out
	}
	if len(pods.Items) == 0 {
		out.Status = "Missing"
		out.Message = "Pod not created"
		return out
	}
	ready := 0
	for i := range pods.Items {
		if monitoringPodReady(&pods.Items[i]) {
			ready++
		}
	}
	out.Ready = ready == len(pods.Items)
	if out.Ready {
		out.Status = "Ready"
	} else {
		out.Status = "Progressing"
	}
	out.Message = fmt.Sprintf("%d/%d ready", ready, len(pods.Items))
	return out
}

func monitoringStackReady(components []v1.MonitoringStackComponentStatus) bool {
	required := map[string]bool{
		"Prometheus Operator": true,
		"Prometheus":          true,
		"Alertmanager":        true,
	}
	for _, component := range components {
		if component.Kind == "CustomResourceDefinition" && !component.Ready {
			return false
		}
		if required[component.Name] && !component.Ready {
			return false
		}
	}
	return true
}

func monitoringDeploymentRolloutStatus(deploy *appsv1.Deployment) string {
	if deploy == nil {
		return "Missing"
	}
	if deploy.Status.ReadyReplicas > 0 && deploy.Status.ReadyReplicas == deploy.Status.Replicas {
		return "Ready"
	}
	return "Progressing"
}

func monitoringPodReady(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func monitoringGVRForKind(kind string) (schema.GroupVersionResource, error) {
	switch kind {
	case "Namespace":
		return schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, nil
	case "ConfigMap":
		return schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, nil
	case "Secret":
		return schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, nil
	case "Service":
		return schema.GroupVersionResource{Version: "v1", Resource: "services"}, nil
	case "ServiceAccount":
		return schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}, nil
	case "Deployment":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, nil
	case "StatefulSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, nil
	case "Role":
		return schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}, nil
	case "RoleBinding":
		return schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}, nil
	case "ClusterRole":
		return schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}, nil
	case "ClusterRoleBinding":
		return schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}, nil
	case "CustomResourceDefinition":
		return schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}, nil
	case "Prometheus":
		return schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheuses"}, nil
	case "PrometheusAgent":
		return schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1alpha1", Resource: "prometheusagents"}, nil
	case "Alertmanager":
		return schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "alertmanagers"}, nil
	case "ThanosRuler":
		return schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "thanosrulers"}, nil
	case "ServiceMonitor":
		return schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "servicemonitors"}, nil
	case "PodMonitor":
		return schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "podmonitors"}, nil
	case "Probe":
		return schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "probes"}, nil
	case "ScrapeConfig":
		return schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1alpha1", Resource: "scrapeconfigs"}, nil
	case "PrometheusRule":
		return schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules"}, nil
	case "AlertmanagerConfig":
		return schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1alpha1", Resource: "alertmanagerconfigs"}, nil
	default:
		return schema.GroupVersionResource{}, fmt.Errorf("unsupported monitoring manifest kind %q", kind)
	}
}

func monitoringKindNamespaced(kind string) bool {
	switch kind {
	case "Namespace", "CustomResourceDefinition", "ClusterRole", "ClusterRoleBinding":
		return false
	default:
		return true
	}
}

func monitoringStackNamespace(stack *v1.KDBMonitoringStack) string {
	if stack != nil && strings.TrimSpace(stack.Spec.Namespace) != "" {
		return strings.TrimSpace(stack.Spec.Namespace)
	}
	return defaultMonitoringStackNamespace
}

func monitoringOperatorVersion(stack *v1.KDBMonitoringStack) string {
	if stack != nil && strings.TrimSpace(stack.Spec.OperatorVersion) != "" {
		return strings.TrimSpace(stack.Spec.OperatorVersion)
	}
	return defaultMonitoringOperatorVer
}

func monitoringBundleURL(stack *v1.KDBMonitoringStack) string {
	if stack != nil && strings.TrimSpace(stack.Spec.BundleURL) != "" {
		return strings.TrimSpace(stack.Spec.BundleURL)
	}
	version := monitoringOperatorVersion(stack)
	return fmt.Sprintf("https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/%s/bundle.yaml", version)
}

func monitoringStackLabels(component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "kdb-monitoring",
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": "kdb-operator",
		"kdb.io/monitoring-stack":      defaultMonitoringStackName,
	}
}

func monitoringResourceRequirements(resources corev1.ResourceRequirements, requests, limits map[string]string) map[string]any {
	if len(resources.Requests) > 0 || len(resources.Limits) > 0 {
		out := map[string]any{}
		if len(resources.Requests) > 0 {
			out["requests"] = resourceListToStringMap(resources.Requests)
		}
		if len(resources.Limits) > 0 {
			out["limits"] = resourceListToStringMap(resources.Limits)
		}
		return out
	}
	return map[string]any{"requests": requests, "limits": limits}
}

func resourceListToStringMap(list corev1.ResourceList) map[string]string {
	out := make(map[string]string, len(list))
	for name, quantity := range list {
		out[string(name)] = quantity.String()
	}
	return out
}

func mergeMonitoringStringMap(base, override map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

func requeueWhenNotReady(ready bool) time.Duration {
	if ready {
		return 0
	}
	return 15 * time.Second
}

func firstNonEmptyMonitoring(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
