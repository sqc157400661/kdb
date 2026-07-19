package controller

import (
	"context"
	"fmt"
	"time"

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
)

const DBRestoreControllerName = "dbrestore-controller"
const restoreOperationAnnotation = "kdb.com/restore-operation-id"

// +kubebuilder:rbac:groups=kdb.com,resources=dbrestores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kdb.com,resources=dbrestores/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kdb.com,resources=dbbackups,verbs=get;list;watch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbinstances,verbs=get;list;watch;create

type DBRestoreReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *DBRestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	restore := &v1.DBRestore{}
	if err := r.Get(ctx, req.NamespacedName, restore); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if restore.Status.Phase == v1.DBRestorePhaseSucceeded || restore.Status.Phase == v1.DBRestorePhaseFailed {
		return ctrl.Result{}, nil
	}
	if restore.Spec.Mode != "NewInstance" {
		return r.failRestore(ctx, restore, "InPlaceRestoreRejected", "only NewInstance restore is supported")
	}
	backup := &v1.DBBackup{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: restore.Namespace, Name: restore.Spec.BackupRef.Name}, backup); err != nil {
		return r.failRestore(ctx, restore, "BackupUnavailable", err.Error())
	}
	if backup.Status.Phase != v1.DBBackupPhaseSucceeded || backup.Status.Artifact == nil {
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}
	source := &v1.KDBInstance{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: restore.Namespace, Name: backup.Spec.InstanceRef.Name}, source); err != nil {
		return r.failRestore(ctx, restore, "SourceInstanceUnavailable", err.Error())
	}
	if restore.Status.StartedAt == nil {
		now := metav1.Now()
		restore.Status.StartedAt = &now
	}
	target := &v1.KDBInstance{}
	key := types.NamespacedName{Namespace: restore.Namespace, Name: restore.Spec.TargetInstanceName}
	err := r.Get(ctx, key, target)
	if apierrors.IsNotFound(err) {
		target = &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Labels: map[string]string{"kdb.com/restored-from": backup.Name}, Annotations: map[string]string{restoreOperationAnnotation: restore.Spec.OperationID}}, Spec: source.DeepCopy().Spec}
		if target.Spec.PostgreSQL == nil {
			return r.failRestore(ctx, restore, "InvalidSource", "source is not PostgreSQL")
		}
		target.Spec.PostgreSQL.Restore = &v1.PostgreSQLRestoreBootstrapSpec{OperationID: restore.Spec.OperationID, BackupID: backup.Status.Artifact.BackupID}
		switch restore.Spec.Target.Type {
		case "", "backup":
		case "time":
			target.Spec.PostgreSQL.Restore.TargetType, target.Spec.PostgreSQL.Restore.Target, target.Spec.PostgreSQL.Restore.TargetAction = "time", postgresRestoreTargetTime(restore.Spec.Target.Value), "promote"
		case "lsn":
			target.Spec.PostgreSQL.Restore.TargetType, target.Spec.PostgreSQL.Restore.Target, target.Spec.PostgreSQL.Restore.TargetAction = "lsn", restore.Spec.Target.Value, "promote"
		case "restore-point":
			target.Spec.PostgreSQL.Restore.TargetType, target.Spec.PostgreSQL.Restore.Target, target.Spec.PostgreSQL.Restore.TargetAction = "name", restore.Spec.Target.Value, "promote"
		default:
			return r.failRestore(ctx, restore, "InvalidRestoreTarget", fmt.Sprintf("unsupported target type %q", restore.Spec.Target.Type))
		}
		if err := r.Create(ctx, target); err != nil {
			return ctrl.Result{}, err
		}
		restore.Status.Phase, restore.Status.ObservedGeneration = v1.DBRestorePhaseRestoring, restore.Generation
		restore.Status.SourceInstanceRef = &corev1.LocalObjectReference{Name: source.Name}
		restore.Status.TargetInstanceRef = &corev1.LocalObjectReference{Name: target.Name}
		restore.Status.RestoredBackupID = backup.Status.Artifact.BackupID
		restore.Status.Message = "target instance created; pgBackRest restore is running"
		setRestoreCondition(&restore.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "RestoreRunning", Message: restore.Status.Message})
		if err := r.Status().Update(ctx, restore); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if target.Annotations[restoreOperationAnnotation] != restore.Spec.OperationID {
		return r.failRestore(ctx, restore, "TargetConflict", "target instance already exists and belongs to another operation")
	}
	if target.Status.PostgreSQL == nil || !target.Status.PostgreSQL.Ready || target.Status.PostgreSQL.Primary == "" {
		restore.Status.Phase, restore.Status.Message = v1.DBRestorePhaseRestoring, "waiting for restored target validation and primary endpoint"
		_ = r.Status().Update(ctx, restore)
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}
	restore.Status.Phase, restore.Status.Message = v1.DBRestorePhaseSucceeded, "NewInstance restore validated and ready"
	now := metav1.Now()
	restore.Status.CompletedAt = &now
	setRestoreCondition(&restore.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "RestoreSucceeded", Message: restore.Status.Message})
	if err := r.Status().Update(ctx, restore); err != nil {
		return ctrl.Result{}, err
	}
	if source.Status.PostgreSQL != nil && source.Status.PostgreSQL.PGBackRest != nil {
		before := source.DeepCopy()
		source.Status.PostgreSQL.PGBackRest.LastRestoreValidationTime = &now
		source.Status.PostgreSQL.PGBackRest.LastRestoreValidationStatus = "Passed"
		_ = r.Status().Patch(ctx, source, client.MergeFrom(before))
	}
	return ctrl.Result{}, nil
}

func postgresRestoreTargetTime(value string) string {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.Format("2006-01-02 15:04:05-07:00")
	}
	return value
}

func (r *DBRestoreReconciler) failRestore(ctx context.Context, restore *v1.DBRestore, reason, message string) (ctrl.Result, error) {
	restore.Status.Phase, restore.Status.Message = v1.DBRestorePhaseFailed, message
	now := metav1.Now()
	restore.Status.CompletedAt = &now
	setRestoreCondition(&restore.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: reason, Message: message})
	return ctrl.Result{}, r.Status().Update(ctx, restore)
}
func setRestoreCondition(conditions *[]metav1.Condition, value metav1.Condition) {
	value.LastTransitionTime = metav1.Now()
	for i := range *conditions {
		if (*conditions)[i].Type == value.Type {
			(*conditions)[i] = value
			return
		}
	}
	*conditions = append(*conditions, value)
}
func (r *DBRestoreReconciler) targetRestores(object client.Object) []reconcile.Request {
	instance, ok := object.(*v1.KDBInstance)
	if !ok {
		return nil
	}
	list := &v1.DBRestoreList{}
	if r.List(context.Background(), list, client.InNamespace(instance.Namespace)) != nil {
		return nil
	}
	out := []reconcile.Request{}
	for i := range list.Items {
		if list.Items[i].Spec.TargetInstanceName == instance.Name && list.Items[i].Status.Phase != v1.DBRestorePhaseSucceeded && list.Items[i].Status.Phase != v1.DBRestorePhaseFailed {
			out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: list.Items[i].Namespace, Name: list.Items[i].Name}})
		}
	}
	return out
}
func (r *DBRestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var options controller.Options
	return builder.ControllerManagedBy(mgr).For(&v1.DBRestore{}).WithOptions(options).Watches(&source.Kind{Type: &v1.KDBInstance{}}, handler.EnqueueRequestsFromMapFunc(r.targetRestores)).Complete(r)
}
