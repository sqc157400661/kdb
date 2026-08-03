package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/postgresqlruntime"
)

const DBFailoverControllerName = "dbfailover-controller"

// +kubebuilder:rbac:groups=kdb.com,resources=dbfailovers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kdb.com,resources=dbfailovers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbinstances,verbs=get;list;watch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch

type DBFailoverReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HTTPClient *http.Client
}

func (r *DBFailoverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	operation := &v1.DBFailover{}
	if err := r.Get(ctx, req.NamespacedName, operation); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if terminalDBFailoverPhase(operation.Status.Phase) {
		return ctrl.Result{}, nil
	}
	instance := &v1.KDBInstance{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: operation.Namespace, Name: operation.Spec.InstanceRef.Name}, instance); err != nil {
		if apierrors.IsNotFound(err) {
			return r.fail(ctx, operation, "InstanceNotFound", err.Error())
		}
		return ctrl.Result{}, err
	}
	if instance.Spec.Engine != "postgresql" || instance.Status.PostgreSQL == nil {
		return r.fail(ctx, operation, "PostgreSQLStatusUnavailable", "target is not an observed PostgreSQL instance")
	}
	pg := instance.Status.PostgreSQL
	if operation.Status.Phase == v1.DBFailoverPhaseRunning {
		if operation.Spec.Mode == "dr-promote" {
			if pg.DR != nil && pg.DR.RuntimeRole == "active" && pg.DR.ActiveClusterID == operation.Spec.ClusterID && pg.DR.Term > operation.Status.PreviousTerm && pg.Primary != "" && pg.Ready {
				if err := r.recordDRDrill(ctx, operation, instance); err != nil {
					return ctrl.Result{}, err
				}
				return r.succeed(ctx, operation, pg.Primary, pg.DR.Term, "DR promotion completed and write routing is ready")
			}
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
		if operation.Spec.Mode == "switchover" || operation.Spec.Mode == "failover" {
			if pg.Term > operation.Status.PreviousTerm && pg.Primary == operation.Spec.Candidate && pg.Ready {
				return r.succeed(ctx, operation, pg.Primary, pg.Term, "HA transition completed")
			}
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
		return r.succeed(ctx, operation, pg.Primary, pg.Term, "node action accepted by kdb-ha")
	}
	if duplicate, err := r.duplicateOperation(ctx, operation); err != nil {
		return ctrl.Result{}, err
	} else if duplicate != "" {
		return r.fail(ctx, operation, "DuplicateOperationID", fmt.Sprintf("operationId is already owned by %s", duplicate))
	}
	observedTerm := pg.Term
	if (operation.Spec.Mode == "dr-fence" || operation.Spec.Mode == "dr-promote") && pg.DR != nil {
		observedTerm = pg.DR.Term
	}
	if operation.Spec.ExpectedTerm > 0 && operation.Spec.ExpectedTerm != observedTerm {
		return r.fail(ctx, operation, "TermMismatch", fmt.Sprintf("expected term %d, current term %d", operation.Spec.ExpectedTerm, observedTerm))
	}
	if operation.Spec.Mode == "failover" && operation.Spec.Force {
		fenced, err := r.fencedTermVerified(ctx, operation, instance)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !fenced {
			return r.wait(ctx, operation, v1.DBFailoverPhaseWaitingForApproval, fmt.Sprintf("former primary term %d lacks Operator fencing confirmation", pg.Term), pg)
		}
	}
	target, phase, reason := validateDBFailover(operation, instance)
	if phase != "" {
		if phase == v1.DBFailoverPhaseFailed {
			return r.fail(ctx, operation, "UnsupportedMode", reason)
		}
		return r.wait(ctx, operation, phase, reason, pg)
	}
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: operation.Namespace, Name: target}, pod); err != nil {
		return ctrl.Result{}, err
	}
	if pod.Status.PodIP == "" {
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}
	if err := r.submit(ctx, operation, instance, pod.Name); err != nil {
		return r.fail(ctx, operation, "KDBHARejected", err.Error())
	}
	now := metav1.Now()
	if operation.Spec.Mode == "dr-fence" {
		operation.Status = v1.DBFailoverStatus{
			ObservedGeneration: operation.Generation, PreviousPrimary: pg.Primary, CurrentPrimary: pg.Primary,
			PreviousTerm: observedTerm, CurrentTerm: observedTerm, EventID: operation.Spec.OperationID, StartedAt: &now,
			Steps: []v1.DBFailoverStepStatus{step("precheck", "Succeeded", "active cluster identity and exact global term validated"), step("fast-stop", "Succeeded", "PostgreSQL stopped before etcd3 fencing evidence was written"), step("fence-term", "Succeeded", "global active term fenced with compare-and-swap")},
		}
		return r.succeed(ctx, operation, pg.Primary, observedTerm, "active cluster PostgreSQL stopped and DR term fenced in etcd3")
	}
	operation.Status = v1.DBFailoverStatus{
		Phase: v1.DBFailoverPhaseRunning, ObservedGeneration: operation.Generation,
		PreviousPrimary: pg.Primary, CurrentPrimary: pg.Primary, PreviousTerm: observedTerm, CurrentTerm: observedTerm,
		EventID: operation.Spec.OperationID, Message: "operation accepted by kdb-ha", StartedAt: &now,
		ApprovalRef: operation.Spec.ApprovalRef, Approvers: append([]string(nil), operation.Spec.Approvers...),
		Steps: []v1.DBFailoverStepStatus{
			step("precheck", "Succeeded", "term, candidate and approval policy validated"),
			step("submit", "Succeeded", "kdb-ha accepted the idempotent operation"),
			step("observe", "Running", "waiting for projected runtime state"),
		},
	}
	setDBFailoverCondition(&operation.Status.Conditions, metav1.Condition{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "KDBHAAccepted", Message: operation.Status.Message, ObservedGeneration: operation.Generation})
	if err := r.Status().Update(ctx, operation); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: time.Second}, nil
}

func (r *DBFailoverReconciler) recordDRDrill(ctx context.Context, operation *v1.DBFailover, instance *v1.KDBInstance) error {
	if instance.Status.PostgreSQL == nil || instance.Status.PostgreSQL.DR == nil {
		return nil
	}
	now := metav1.Now()
	started := operation.Status.StartedAt
	if started == nil {
		started = &now
	}
	rto := int64(now.Sub(started.Time).Seconds())
	if rto < 0 {
		rto = 0
	}
	dr := instance.Status.PostgreSQL.DR
	dr.LastDrill = &v1.PostgreSQLDRDrillStatus{
		OperationID: operation.Spec.OperationID, ApprovalRef: operation.Spec.ApprovalRef,
		Approvers: append([]string(nil), operation.Spec.Approvers...), PreviousTerm: operation.Status.PreviousTerm,
		CurrentTerm: dr.Term, RPOBytes: dr.RPOBytes, RPOSeconds: dr.RPOSeconds, RTOSeconds: rto,
		StartedAt: started, CompletedAt: &now,
	}
	operation.Status.EstimatedDataLossBytes = dr.RPOBytes
	operation.Status.ObservedRPOSeconds = dr.RPOSeconds
	operation.Status.RTOSeconds = rto
	return r.Status().Update(ctx, instance)
}

func (r *DBFailoverReconciler) fencedTermVerified(ctx context.Context, operation *v1.DBFailover, instance *v1.KDBInstance) (bool, error) {
	if instance.Status.PostgreSQL == nil || operation.Spec.FencedTerm < instance.Status.PostgreSQL.Term {
		return false, nil
	}
	lease := &coordinationv1.Lease{}
	key := types.NamespacedName{Namespace: instance.Namespace, Name: naming.PostgreSQLLeaderLeaseName(instance.Name)}
	if err := r.Get(ctx, key, lease); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return lease.Annotations["kdb.com/fenced-term"] == strconv.FormatInt(instance.Status.PostgreSQL.Term, 10), nil
}

func validateDBFailover(operation *v1.DBFailover, instance *v1.KDBInstance) (string, string, string) {
	pg := instance.Status.PostgreSQL
	mode := operation.Spec.Mode
	switch mode {
	case "switchover", "failover":
		member, ok := eligibleReplica(pg.Members, operation.Spec.Candidate)
		if !ok {
			return "", v1.DBFailoverPhaseWaitingForCandidate, "no eligible healthy candidate; automatic promotion is stopped"
		}
		if mode == "switchover" && pg.Primary == "" {
			return "", v1.DBFailoverPhaseWaitingForCandidate, "healthy primary is required for switchover"
		}
		if operation.Spec.Force {
			if operation.Spec.DataLossAcceptance != "AcceptPotentialDataLoss" || strings.TrimSpace(operation.Spec.ApprovalRef) == "" || uniqueStrings(operation.Spec.Approvers) < 2 {
				return "", v1.DBFailoverPhaseWaitingForApproval, "forced failover requires explicit data-loss acceptance and two distinct approvers"
			}
			if operation.Spec.FencedTerm < pg.Term {
				return "", v1.DBFailoverPhaseWaitingForApproval, fmt.Sprintf("former primary term %d is not fenced", pg.Term)
			}
		}
		return member.Name, "", ""
	case "pause":
		if pg.Primary == "" {
			return "", v1.DBFailoverPhaseWaitingForCandidate, "primary is unavailable"
		}
		return pg.Primary, "", ""
	case "resume":
		if pg.Primary != "" {
			return pg.Primary, "", ""
		}
		for _, pod := range instance.Status.InstanceSet.PodInfos {
			if pod.PodName != "" && pod.PodPhase == corev1.PodRunning {
				return pod.PodName, "", ""
			}
		}
		return "", v1.DBFailoverPhaseWaitingForCandidate, "no running instance Pod is available to clear pause"
	case "restart":
		target := operation.Spec.Candidate
		if target == "" {
			target = pg.Primary
		}
		if !memberReady(pg.Members, target) {
			return "", v1.DBFailoverPhaseWaitingForCandidate, "restart target is not ready"
		}
		return target, "", ""
	case "rejoin", "reinit":
		if _, ok := eligibleReplica(pg.Members, operation.Spec.Candidate); !ok {
			return "", v1.DBFailoverPhaseWaitingForCandidate, "rejoin/reinit requires a ready replica target"
		}
		return operation.Spec.Candidate, "", ""
	case "dr-fence":
		if pg.DR == nil || !pg.DR.Enabled || pg.DR.RuntimeRole != "active" || pg.DR.ActiveClusterID != pg.DR.ClusterID {
			return "", v1.DBFailoverPhaseWaitingForCandidate, "only the observed active DR cluster can fence itself"
		}
		if operation.Spec.ClusterID != pg.DR.ClusterID || operation.Spec.ExpectedTerm != pg.DR.Term || strings.TrimSpace(operation.Spec.OperationID) == "" {
			return "", v1.DBFailoverPhaseWaitingForApproval, "DR fencing requires the local clusterId, operationId and exact global term"
		}
		if pg.Primary == "" {
			return "", v1.DBFailoverPhaseWaitingForCandidate, "active PostgreSQL primary is unavailable for fast-stop fencing"
		}
		return pg.Primary, "", ""
	case "dr-promote":
		if pg.DR == nil || !pg.DR.Enabled || pg.DR.RuntimeRole != "standby" || pg.DR.ActiveClusterID == pg.DR.ClusterID {
			return "", v1.DBFailoverPhaseWaitingForCandidate, "only the observed remote standby cluster can be promoted"
		}
		if operation.Spec.ClusterID != pg.DR.ClusterID || operation.Spec.ExpectedTerm != pg.DR.Term {
			return "", v1.DBFailoverPhaseWaitingForApproval, "DR promotion requires the local clusterId and exact global term"
		}
		if !operation.Spec.Force || operation.Spec.DataLossAcceptance != "AcceptPotentialDataLoss" || strings.TrimSpace(operation.Spec.ApprovalRef) == "" || uniqueStrings(operation.Spec.Approvers) < 2 {
			return "", v1.DBFailoverPhaseWaitingForApproval, "DR promotion requires explicit data-loss acceptance and two distinct approvers"
		}
		if pg.DR.FencedClusterID != pg.DR.ActiveClusterID || pg.DR.FencedTerm != pg.DR.Term || operation.Spec.FencedTerm != pg.DR.Term {
			return "", v1.DBFailoverPhaseWaitingForApproval, "the former active cluster exact term is not fenced in etcd3"
		}
		for _, member := range pg.Members {
			if member.Ready && member.Running && member.Role == "standby_leader" {
				return member.Name, "", ""
			}
		}
		return "", v1.DBFailoverPhaseWaitingForCandidate, "no healthy local standby leader is available"
	default:
		return "", v1.DBFailoverPhaseFailed, "unsupported PostgreSQL HA operation"
	}
}

func (r *DBFailoverReconciler) submit(ctx context.Context, op *v1.DBFailover, instance *v1.KDBInstance, podName string) error {
	body := map[string]any{
		"requestId": op.Name, "operationId": op.Spec.OperationID, "expectedTerm": op.Spec.ExpectedTerm,
		"candidate": op.Spec.Candidate, "force": op.Spec.Force, "dataLossAcceptance": op.Spec.DataLossAcceptance,
		"approvalRef": op.Spec.ApprovalRef, "approvers": op.Spec.Approvers, "fencedTerm": op.Spec.FencedTerm,
		"mode": op.Spec.RestartMode, "reason": op.Spec.Reason,
		"clusterId": op.Spec.ClusterID,
	}
	if instance.Status.PostgreSQL != nil {
		body["leader"] = instance.Status.PostgreSQL.Primary
	}
	data, _ := json.Marshal(body)
	endpoint := postgresqlruntime.PodEndpoint(instance, podName)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/v1/postgresql/actions/%s", endpoint, op.Spec.Mode), bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	client := r.HTTPClient
	username, password := "", ""
	if client == nil {
		var err error
		client, username, password, err = postgresqlruntime.Client(ctx, r.Client, instance, 10*time.Second)
		if err != nil {
			return err
		}
	}
	if username != "" || password != "" {
		request.SetBasicAuth(username, password)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		return fmt.Errorf("kdb-ha returned %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	return nil
}

func (r *DBFailoverReconciler) duplicateOperation(ctx context.Context, current *v1.DBFailover) (string, error) {
	list := &v1.DBFailoverList{}
	if err := r.List(ctx, list, client.InNamespace(current.Namespace)); err != nil {
		return "", err
	}
	for i := range list.Items {
		item := &list.Items[i]
		if item.UID != current.UID && item.Spec.OperationID == current.Spec.OperationID && item.CreationTimestamp.Before(&current.CreationTimestamp) {
			return item.Name, nil
		}
	}
	return "", nil
}

func (r *DBFailoverReconciler) wait(ctx context.Context, op *v1.DBFailover, phase, message string, pg *v1.PostgreSQLStatus) (ctrl.Result, error) {
	op.Status.Phase, op.Status.Message, op.Status.EventID = phase, message, op.Spec.OperationID
	op.Status.ObservedGeneration, op.Status.CurrentPrimary, op.Status.CurrentTerm = op.Generation, pg.Primary, pg.Term
	conditionType := "CandidateAvailable"
	if phase == v1.DBFailoverPhaseWaitingForApproval {
		conditionType = "ApprovedAndFenced"
	}
	setDBFailoverCondition(&op.Status.Conditions, metav1.Condition{Type: conditionType, Status: metav1.ConditionFalse, Reason: phase, Message: message, ObservedGeneration: op.Generation})
	if err := r.Status().Update(ctx, op); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *DBFailoverReconciler) fail(ctx context.Context, op *v1.DBFailover, reason, message string) (ctrl.Result, error) {
	now := metav1.Now()
	op.Status.Phase, op.Status.Message, op.Status.EventID, op.Status.CompletedAt = v1.DBFailoverPhaseFailed, message, op.Spec.OperationID, &now
	setDBFailoverCondition(&op.Status.Conditions, metav1.Condition{Type: "Succeeded", Status: metav1.ConditionFalse, Reason: reason, Message: message, ObservedGeneration: op.Generation})
	return ctrl.Result{}, r.Status().Update(ctx, op)
}

func (r *DBFailoverReconciler) succeed(ctx context.Context, op *v1.DBFailover, primary string, term int64, message string) (ctrl.Result, error) {
	now := metav1.Now()
	op.Status.Phase, op.Status.Message, op.Status.CurrentPrimary, op.Status.CurrentTerm, op.Status.CompletedAt = v1.DBFailoverPhaseSucceeded, message, primary, term, &now
	op.Status.Steps = append(op.Status.Steps, step("observe", "Succeeded", message))
	setDBFailoverCondition(&op.Status.Conditions, metav1.Condition{Type: "Succeeded", Status: metav1.ConditionTrue, Reason: "OperationCompleted", Message: message, ObservedGeneration: op.Generation})
	return ctrl.Result{}, r.Status().Update(ctx, op)
}

func eligibleReplica(members []v1.PostgreSQLMemberStatus, name string) (v1.PostgreSQLMemberStatus, bool) {
	for _, m := range members {
		if m.Name == name && m.Ready && m.Running && m.Role == "replica" {
			return m, true
		}
	}
	return v1.PostgreSQLMemberStatus{}, false
}
func memberReady(members []v1.PostgreSQLMemberStatus, name string) bool {
	for _, m := range members {
		if m.Name == name {
			return m.Ready && m.Running
		}
	}
	return false
}
func uniqueStrings(values []string) int {
	seen := map[string]struct{}{}
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			seen[v] = struct{}{}
		}
	}
	return len(seen)
}
func terminalDBFailoverPhase(phase string) bool {
	return phase == v1.DBFailoverPhaseSucceeded || phase == v1.DBFailoverPhaseFailed
}
func step(name, phase, message string) v1.DBFailoverStepStatus {
	return v1.DBFailoverStepStatus{Name: name, Phase: phase, Message: message, UpdatedAt: metav1.Now()}
}
func setDBFailoverCondition(conditions *[]metav1.Condition, value metav1.Condition) {
	value.LastTransitionTime = metav1.Now()
	for i := range *conditions {
		if (*conditions)[i].Type == value.Type {
			(*conditions)[i] = value
			return
		}
	}
	*conditions = append(*conditions, value)
}

func (r *DBFailoverReconciler) instanceOperations(object client.Object) []reconcile.Request {
	instance, ok := object.(*v1.KDBInstance)
	if !ok {
		return nil
	}
	list := &v1.DBFailoverList{}
	if err := r.List(context.Background(), list, client.InNamespace(instance.Namespace)); err != nil {
		return nil
	}
	requests := []reconcile.Request{}
	for i := range list.Items {
		if list.Items[i].Spec.InstanceRef.Name == instance.Name && !terminalDBFailoverPhase(list.Items[i].Status.Phase) {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: list.Items[i].Namespace, Name: list.Items[i].Name}})
		}
	}
	return requests
}

func (r *DBFailoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var options controller.Options
	return builder.ControllerManagedBy(mgr).For(&v1.DBFailover{}).WithOptions(options).
		Watches(&source.Kind{Type: &v1.KDBInstance{}}, handler.EnqueueRequestsFromMapFunc(r.instanceOperations)).Complete(r)
}
