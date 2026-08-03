package controller

import (
	"reflect"
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsLogSystemDeploymentReadyRequiresCurrentRollout(t *testing.T) {
	tests := []struct {
		name      string
		deploy    appsv1.Deployment
		wantReady bool
	}{
		{
			name: "old generation ready replicas are not enough",
			deploy: appsv1.Deployment{
				Status: appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2},
			},
			wantReady: false,
		},
		{
			name: "updated replicas must match desired",
			deploy: appsv1.Deployment{
				Status: appsv1.DeploymentStatus{ObservedGeneration: 2, UpdatedReplicas: 1, ReadyReplicas: 2, AvailableReplicas: 2},
			},
			wantReady: false,
		},
		{
			name: "available replicas must match desired",
			deploy: appsv1.Deployment{
				Status: appsv1.DeploymentStatus{ObservedGeneration: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 1},
			},
			wantReady: false,
		},
		{
			name: "current generation fully rolled out",
			deploy: appsv1.Deployment{
				Status: appsv1.DeploymentStatus{ObservedGeneration: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2},
			},
			wantReady: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.deploy.Generation = 2
			if got := isLogSystemDeploymentReady(&tt.deploy, 2); got != tt.wantReady {
				t.Fatalf("isLogSystemDeploymentReady() = %v, want %v", got, tt.wantReady)
			}
		})
	}
}

func TestRenderLogSystemFluentBitConfigCollectsContainerAndDatabaseFileLogs(t *testing.T) {
	conf := renderLogSystemFluentBitConfig("http://loki.kdb.svc:3100/loki/api/v1/push", []string{"/custom/database-logs", "relative", "/kdbdata/log/"})
	for _, want := range []string{
		"Host        loki.kdb.svc",
		"Port        3100",
		"URI         /loki/api/v1/push",
		"/var/log/containers/*.log",
		"/var/lib/kubelet/pods/*/volumes/*/*/mount/log/*.log",
		"/var/local-path-provisioner/*/log/*.log",
		"/var/lib/kubelet/pods/*/volumes/*/*/custom/database-logs/*.log",
		"/var/lib/kubelet/pods/*/volumes/*/*/mount/custom/database-logs/*.log",
		"Parser            database_file",
		"Tag               kdb.database.file",
		"Label_Keys  $kubernetes['namespace_name'],$kubernetes['pod_name'],$kubernetes['container_name'],$kubernetes['host'],$pod_uid,$pod_name,$pod_namespace,$file_path",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("fluent-bit config missing %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "relative") {
		t.Fatalf("fluent-bit config should ignore relative extra log dirs:\n%s", conf)
	}
	if strings.Contains(conf, "/kdbdata/log/*.log") {
		t.Fatalf("host collector must not treat the container mount point as a PVC subdirectory:\n%s", conf)
	}
	for _, legacy := range []string{"/kdb/logs/*.log", "/volumes/*/*/log/*.log", "/volumes/*/*/logs/*.log"} {
		if strings.Contains(conf, legacy) {
			t.Fatalf("fluent-bit config should not retain legacy default %q:\n%s", legacy, conf)
		}
	}
	if strings.Contains(conf, "mysql_file") || strings.Contains(conf, "kdb.mysql.file") {
		t.Fatalf("database file input must not retain MySQL-only parser or tag:\n%s", conf)
	}
	parsers := renderLogSystemFluentBitParsers()
	if !strings.Contains(parsers, "Name        cri") || !strings.Contains(parsers, "Name        database_file") {
		t.Fatalf("parsers missing cri or database_file parser:\n%s", parsers)
	}
}

func TestRenderLogSystemPodScopeLuaMapsLocalPathPVCToCurrentPod(t *testing.T) {
	script := renderLogSystemPodScopeLua(map[string]logPodScope{
		"mysql-a-kdb-data": {PodUID: "pod-uid-a", PodName: "mysql-a-0", Namespace: "kdb"},
	})
	for _, want := range []string{
		`["mysql-a-kdb-data"] = { pod_uid = "pod-uid-a", pod_name = "mysql-a-0", pod_namespace = "kdb" }`,
		`/pods/([^/]+)/volumes/`,
		`/var/local%-path%-provisioner/`,
		`set_if_empty(record, "pod_uid", scope.pod_uid)`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("pod scope Lua missing %q:\n%s", want, script)
		}
	}
}

func TestRenderLogSystemLokiOutputEndpointEnablesTLSForHTTPS(t *testing.T) {
	conf := renderLogSystemLokiOutputEndpoint("https://loki.example.com/loki/api/v1/push")
	for _, want := range []string{
		"Host        loki.example.com",
		"Port        443",
		"URI         /loki/api/v1/push",
		"TLS         On",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("loki endpoint config missing %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "Url") {
		t.Fatalf("loki endpoint config must not use unsupported Url property:\n%s", conf)
	}
}

func TestLogSystemSelectorLabelsExcludeBackendHash(t *testing.T) {
	logSystem := &v1.KDBLogSystem{
		ObjectMeta: metav1.ObjectMeta{Name: "kdb-logs"},
		Spec:       v1.KDBLogSystemSpec{BackendID: "backend-a"},
	}

	selectorLabels := logSystemSelectorLabels(logSystem)
	if _, ok := selectorLabels["kdb.com/log-backend-hash"]; ok {
		t.Fatalf("selector labels must not include mutable backend hash: %v", selectorLabels)
	}

	want := map[string]string{
		"app.kubernetes.io/name":       "kdb-log-system",
		"app.kubernetes.io/managed-by": "kdb-operator",
		"app.kubernetes.io/instance":   "kdb-logs",
	}
	if !reflect.DeepEqual(selectorLabels, want) {
		t.Fatalf("logSystemSelectorLabels() = %v, want %v", selectorLabels, want)
	}
}

func TestLogSystemLabelsIncludeSelectorLabelsAndBackendHash(t *testing.T) {
	logSystem := &v1.KDBLogSystem{
		ObjectMeta: metav1.ObjectMeta{Name: "kdb-logs"},
		Spec:       v1.KDBLogSystemSpec{BackendID: "backend-a"},
	}

	labels := logSystemLabels(logSystem)
	for key, value := range logSystemSelectorLabels(logSystem) {
		if labels[key] != value {
			t.Fatalf("logSystemLabels()[%q] = %q, want selector value %q", key, labels[key], value)
		}
	}
	if labels["kdb.com/log-backend-hash"] == "" {
		t.Fatalf("logSystemLabels() missing backend hash: %v", labels)
	}
}

func TestMergeLogSystemLabelsKeepsExistingSelectorLabels(t *testing.T) {
	base := map[string]string{
		"app.kubernetes.io/name":       "kdb-log-system",
		"app.kubernetes.io/managed-by": "kdb-operator",
		"app.kubernetes.io/instance":   "kdb-logs",
		"kdb.com/log-backend-hash":     "new-hash",
	}
	existingSelector := map[string]string{
		"app.kubernetes.io/name":       "kdb-log-system",
		"app.kubernetes.io/managed-by": "kdb-operator",
		"kdb.com/log-backend-hash":     "old-hash",
	}

	labels := mergeLogSystemLabels(base, existingSelector)
	if labels["kdb.com/log-backend-hash"] != "old-hash" {
		t.Fatalf("template labels must preserve existing immutable selector hash, got %q", labels["kdb.com/log-backend-hash"])
	}
	if labels["app.kubernetes.io/instance"] != "kdb-logs" {
		t.Fatalf("template labels must include stable service selector labels, got %v", labels)
	}
	if base["kdb.com/log-backend-hash"] != "new-hash" {
		t.Fatalf("mergeLogSystemLabels mutated base labels: %v", base)
	}
}
