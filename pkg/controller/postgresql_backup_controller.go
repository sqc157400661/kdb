package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
	"github.com/sqc157400661/kdb/internal/postgresqlruntime"
)

const DBBackupControllerName = "dbbackup-controller"

// +kubebuilder:rbac:groups=kdb.com,resources=dbbackups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kdb.com,resources=dbbackups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbinstances,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbinstances/status,verbs=get;update;patch

type DBBackupReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HTTPClient *http.Client
	Endpoint   func(*v1.KDBInstance) string
}

type runtimeBackupOperation struct {
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Result struct {
		BackupID      string    `json:"backupId"`
		Type          string    `json:"type"`
		Stanza        string    `json:"stanza"`
		StartTime     time.Time `json:"startTime"`
		StopTime      time.Time `json:"stopTime"`
		StartLSN      string    `json:"startLsn"`
		StopLSN       string    `json:"stopLsn"`
		SizeBytes     int64     `json:"sizeBytes"`
		RepoSizeBytes int64     `json:"repoSizeBytes"`
	} `json:"result"`
}

func (r *DBBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	backup := &v1.DBBackup{}
	if err := r.Get(ctx, req.NamespacedName, backup); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if backup.Status.Phase == v1.DBBackupPhaseSucceeded || backup.Status.Phase == v1.DBBackupPhaseFailed {
		return ctrl.Result{}, nil
	}
	instance := &v1.KDBInstance{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: backup.Namespace, Name: backup.Spec.InstanceRef.Name}, instance); err != nil {
		return r.failBackup(ctx, backup, "InstanceUnavailable", err.Error())
	}
	if !strings.EqualFold(instance.Spec.Engine, "postgresql") || instance.Status.PostgreSQL == nil || !instance.Status.PostgreSQL.Ready || instance.Status.PostgreSQL.Primary == "" {
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}
	if backup.Status.Phase == "" || backup.Status.Phase == v1.DBBackupPhasePending {
		now := metav1.Now()
		backup.Status.Phase, backup.Status.StartedAt, backup.Status.ObservedGeneration = v1.DBBackupPhaseRunning, &now, backup.Generation
		backup.Status.ExecutorPod = instance.Status.PostgreSQL.Primary
		setBackupCondition(&backup.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "BackupRunning", Message: "pgBackRest backup is running"})
		if err := r.Status().Update(ctx, backup); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
	endpoint := r.runtimeEndpoint(instance, backup.Status.ExecutorPod)
	body, _ := json.Marshal(map[string]any{"operationId": backup.Spec.OperationID, "type": backup.Spec.Type, "validate": backup.Spec.Validate})
	operation := runtimeBackupOperation{}
	if err := r.runtimeJSON(ctx, instance, http.MethodPost, endpoint+"/v1/postgresql/backup/tasks", body, &operation); err != nil {
		return r.failBackup(ctx, backup, "BackupExecutionFailed", err.Error())
	}
	if operation.Status == "Running" {
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}
	if operation.Status != "Succeeded" {
		message := "pgBackRest backup did not succeed"
		if operation.Error != nil && operation.Error.Message != "" {
			message = operation.Error.Message
		}
		return r.failBackup(ctx, backup, "BackupExecutionFailed", message)
	}
	start, stop := metav1.NewTime(operation.Result.StartTime), metav1.NewTime(operation.Result.StopTime)
	backup.Status.Artifact = &v1.DBBackupArtifactStatus{
		BackupID: operation.Result.BackupID, URI: fmt.Sprintf("pgbackrest://%s/%s", operation.Result.Stanza, operation.Result.BackupID),
		Type: operation.Result.Type, Stanza: operation.Result.Stanza, StartTime: &start, StopTime: &stop,
		StartLSN: operation.Result.StartLSN, StopLSN: operation.Result.StopLSN, SizeBytes: operation.Result.SizeBytes, RepoSizeBytes: operation.Result.RepoSizeBytes,
	}
	backup.Status.WALStart, backup.Status.WALEnd = operation.Result.StartLSN, operation.Result.StopLSN
	backup.Status.PITRWindowStart, backup.Status.PITRWindowEnd = &start, &stop
	backup.Status.ValidationStatus = "NotRequested"
	if backup.Spec.Validate {
		backup.Status.ValidationStatus = "Passed"
	}
	backup.Status.Phase, backup.Status.Message = v1.DBBackupPhaseSucceeded, "pgBackRest backup completed"
	now := metav1.Now()
	backup.Status.CompletedAt = &now
	setBackupCondition(&backup.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "BackupSucceeded", Message: backup.Status.Message})
	if err := r.Status().Update(ctx, backup); err != nil {
		return ctrl.Result{}, err
	}
	_ = r.projectBackupStatus(ctx, instance, backup)
	return ctrl.Result{}, nil
}

func (r *DBBackupReconciler) runtimeEndpoint(instance *v1.KDBInstance, executorPod string) string {
	if r.Endpoint != nil {
		return strings.TrimRight(r.Endpoint(instance), "/")
	}
	if executorPod == "" {
		executorPod = instance.Status.PostgreSQL.Primary
	}
	for _, item := range instance.Status.PostgreSQL.Endpoints {
		if item.PodName == executorPod {
			return postgresqlruntime.PodEndpoint(instance, item.PodName)
		}
	}
	return ""
}

func (r *DBBackupReconciler) runtimeJSON(ctx context.Context, instance *v1.KDBInstance, method, url string, body []byte, output any) error {
	if url == "" {
		return fmt.Errorf("primary kdb-ha endpoint is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	client := r.HTTPClient
	username, password := "", ""
	if client == nil {
		var err error
		client, username, password, err = postgresqlruntime.Client(ctx, r.Client, instance, 30*time.Second)
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
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("kdb-ha returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	if output != nil {
		return json.Unmarshal(data, output)
	}
	return nil
}

func (r *DBBackupReconciler) failBackup(ctx context.Context, backup *v1.DBBackup, reason, message string) (ctrl.Result, error) {
	backup.Status.Phase, backup.Status.Message = v1.DBBackupPhaseFailed, message
	now := metav1.Now()
	backup.Status.CompletedAt = &now
	setBackupCondition(&backup.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: reason, Message: message})
	return ctrl.Result{}, r.Status().Update(ctx, backup)
}

func (r *DBBackupReconciler) projectBackupStatus(ctx context.Context, instance *v1.KDBInstance, backup *v1.DBBackup) error {
	if instance.Status.PostgreSQL == nil || instance.Status.PostgreSQL.PGBackRest == nil {
		return nil
	}
	before := instance.DeepCopy()
	status := instance.Status.PostgreSQL.PGBackRest
	status.LatestBackupRef = backup.Name
	status.LastSuccessfulBackupTime = backup.Status.CompletedAt
	status.PITRWindowStart, status.PITRWindowEnd = backup.Status.PITRWindowStart, backup.Status.PITRWindowEnd
	if backup.Status.ValidationStatus == "Passed" {
		status.LastRestoreValidationTime, status.LastRestoreValidationStatus = backup.Status.CompletedAt, "Passed"
	}
	return r.Status().Patch(ctx, instance, client.MergeFrom(before))
}

func setBackupCondition(conditions *[]metav1.Condition, value metav1.Condition) {
	value.LastTransitionTime = metav1.Now()
	for i := range *conditions {
		if (*conditions)[i].Type == value.Type {
			(*conditions)[i] = value
			return
		}
	}
	*conditions = append(*conditions, value)
}

func (r *DBBackupReconciler) instanceBackups(object client.Object) []reconcile.Request {
	instance, ok := object.(*v1.KDBInstance)
	if !ok {
		return nil
	}
	list := &v1.DBBackupList{}
	if r.List(context.Background(), list, client.InNamespace(instance.Namespace)) != nil {
		return nil
	}
	requests := []reconcile.Request{}
	for i := range list.Items {
		if list.Items[i].Spec.InstanceRef.Name == instance.Name && list.Items[i].Status.Phase != v1.DBBackupPhaseSucceeded && list.Items[i].Status.Phase != v1.DBBackupPhaseFailed {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: list.Items[i].Namespace, Name: list.Items[i].Name}})
		}
	}
	return requests
}

func (r *DBBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var options controller.Options
	return builder.ControllerManagedBy(mgr).For(&v1.DBBackup{}).WithOptions(options).Watches(&source.Kind{Type: &v1.KDBInstance{}}, handler.EnqueueRequestsFromMapFunc(r.instanceBackups)).Complete(r)
}
