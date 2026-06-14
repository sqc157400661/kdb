package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
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
