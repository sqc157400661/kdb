package controller

import (
	"context"
	"testing"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
)

func TestValidateDBFailoverSafetyPolicy(t *testing.T) {
	instance := &v1.KDBInstance{Status: v1.KDBInstanceStatus{PostgreSQL: &v1.PostgreSQLStatus{
		Term: 7, Primary: "pg0", Members: []v1.PostgreSQLMemberStatus{
			{Name: "pg0", Role: "primary", Running: true, Ready: true},
			{Name: "pg1", Role: "replica", Running: true, Ready: true},
		},
	}}}
	op := &v1.DBFailover{Spec: v1.DBFailoverSpec{Mode: "switchover", Candidate: "missing"}}
	if _, phase, _ := validateDBFailover(op, instance); phase != v1.DBFailoverPhaseWaitingForCandidate {
		t.Fatalf("unhealthy candidate phase=%q", phase)
	}
	op.Spec.Mode, op.Spec.Candidate, op.Spec.Force = "failover", "pg1", true
	if _, phase, _ := validateDBFailover(op, instance); phase != v1.DBFailoverPhaseWaitingForApproval {
		t.Fatalf("unapproved force phase=%q", phase)
	}
	op.Spec.DataLossAcceptance, op.Spec.ApprovalRef = "AcceptPotentialDataLoss", "approval-1"
	op.Spec.Approvers, op.Spec.FencedTerm = []string{"alice", "bob"}, 7
	if target, phase, reason := validateDBFailover(op, instance); target != "pg1" || phase != "" {
		t.Fatalf("valid force target=%q phase=%q reason=%q", target, phase, reason)
	}
	instance.Status.PostgreSQL.Primary = ""
	instance.Status.PostgreSQL.Members = nil
	instance.Status.InstanceSet.PodInfos = []shared.PodStatusInfo{{PodName: "pg0", PodPhase: corev1.PodRunning}}
	op.Spec.Mode = "resume"
	if target, phase, reason := validateDBFailover(op, instance); target != "pg0" || phase != "" {
		t.Fatalf("resume without primary target=%q phase=%q reason=%q", target, phase, reason)
	}
	op.Spec.Mode = "pause"
	if _, phase, _ := validateDBFailover(op, instance); phase != v1.DBFailoverPhaseWaitingForCandidate {
		t.Fatalf("pause without primary phase=%q", phase)
	}
}

func TestForcedFailoverRequiresLeaseFencingEvidence(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	_ = coordinationv1.AddToScheme(scheme)
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"}, Status: v1.KDBInstanceStatus{PostgreSQL: &v1.PostgreSQLStatus{Term: 9}}}
	lease := &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: "orders-leader", Namespace: "kdb", Annotations: map[string]string{"kdb.com/fenced-term": "9"}}}
	r := &DBFailoverReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, lease).Build()}
	op := &v1.DBFailover{Spec: v1.DBFailoverSpec{FencedTerm: 9}}
	verified, err := r.fencedTermVerified(context.Background(), op, instance)
	if err != nil || !verified {
		t.Fatalf("verified=%v err=%v", verified, err)
	}
	op.Spec.FencedTerm = 8
	verified, err = r.fencedTermVerified(context.Background(), op, instance)
	if err != nil || verified {
		t.Fatalf("stale verified=%v err=%v", verified, err)
	}
}

func TestValidateDRPromotionRequiresExactFenceAndDualApproval(t *testing.T) {
	instance := &v1.KDBInstance{Status: v1.KDBInstanceStatus{PostgreSQL: &v1.PostgreSQLStatus{
		Ready: true, Members: []v1.PostgreSQLMemberStatus{{Name: "orders-b-0", Role: "standby_leader", Running: true, Ready: true}},
		DR: &v1.PostgreSQLDRStatus{Enabled: true, ClusterID: "cluster-b", PeerClusterID: "cluster-a", RuntimeRole: "standby", ActiveClusterID: "cluster-a", Term: 12},
	}}}
	op := &v1.DBFailover{Spec: v1.DBFailoverSpec{Mode: "dr-promote", OperationID: "dr-1", ClusterID: "cluster-b", ExpectedTerm: 12, Force: true, DataLossAcceptance: "AcceptPotentialDataLoss", ApprovalRef: "approval-1", Approvers: []string{"alice", "bob"}, FencedTerm: 12}}
	if _, phase, _ := validateDBFailover(op, instance); phase != v1.DBFailoverPhaseWaitingForApproval {
		t.Fatalf("unfenced promotion phase=%q", phase)
	}
	instance.Status.PostgreSQL.DR.FencedClusterID, instance.Status.PostgreSQL.DR.FencedTerm = "cluster-a", 12
	if target, phase, reason := validateDBFailover(op, instance); target != "orders-b-0" || phase != "" {
		t.Fatalf("valid promotion target=%q phase=%q reason=%q", target, phase, reason)
	}
	op.Spec.Approvers = []string{"alice", "alice"}
	if _, phase, _ := validateDBFailover(op, instance); phase != v1.DBFailoverPhaseWaitingForApproval {
		t.Fatalf("duplicate approvers phase=%q", phase)
	}
}
