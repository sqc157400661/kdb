package controller

import (
	"reflect"
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
