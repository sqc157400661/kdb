package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
	"github.com/sqc157400661/kdb/internal/naming"
)

const DBRestoreControllerName = "dbrestore-controller"
const restoreOperationAnnotation = "kdb.com/restore-operation-id"
const restoreLocalRepositoryPVCAnnotation = "kdb.com/restore-local-repository-pvc"

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
	if restore.Status.Phase == v1.DBRestorePhaseFailed {
		return ctrl.Result{}, nil
	}
	if restore.Status.Phase == v1.DBRestorePhaseSucceeded {
		return r.cleanupCompletedRestoreBootstrap(ctx, restore)
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
		target = restoredTargetInstance(source, backup, restore)
		if target.Spec.PostgreSQL == nil {
			return r.failRestore(ctx, restore, "InvalidSource", "source is not PostgreSQL")
		}
		if target.Spec.PostgreSQL.Backups != nil && target.Spec.PostgreSQL.Backups.PGBackRest != nil &&
			target.Spec.PostgreSQL.Backups.PGBackRest.RepoType == "local" {
			repositoryPVC, pvcErr := localRepositoryPVCForBackup(backup)
			if pvcErr != nil {
				return r.failRestore(ctx, restore, "LocalRepositoryUnavailable", pvcErr.Error())
			}
			target.Annotations[restoreLocalRepositoryPVCAnnotation] = repositoryPVC
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
		if err := r.ensureRestoredCredentialSecret(ctx, source, target); err != nil {
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
	if err := r.ensureRestoredCredentialSecret(ctx, source, target); err != nil {
		return ctrl.Result{}, err
	}
	if target.Status.PostgreSQL == nil || !target.Status.PostgreSQL.Ready || target.Status.PostgreSQL.Primary == "" {
		restore.Status.Phase, restore.Status.Message = v1.DBRestorePhaseRestoring, "waiting for restored target validation and primary endpoint"
		_ = r.Status().Update(ctx, restore)
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}
	if restoreBootstrapCleanupRequired(target) {
		if err := r.cleanupRestoreBootstrap(ctx, target); err != nil {
			return ctrl.Result{}, err
		}
		restore.Status.Phase, restore.Status.Message = v1.DBRestorePhaseRestoring, "target validated; detaching restore bootstrap and source repository"
		if err := r.Status().Update(ctx, restore); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}
	if !postgresqlAvailableForCurrentGeneration(target) {
		restore.Status.Phase, restore.Status.Message = v1.DBRestorePhaseRestoring, "waiting for restored target rollout after bootstrap cleanup"
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

func (r *DBRestoreReconciler) ensureRestoredCredentialSecret(ctx context.Context, source, target *v1.KDBInstance) error {
	if source == nil || source.Spec.PostgreSQL == nil || target == nil || target.Spec.PostgreSQL == nil {
		return nil
	}
	targetMeta := naming.PostgreSQLCredentialSecret(target)
	if target.Spec.PostgreSQL.CredentialSecretRef != nil && strings.TrimSpace(target.Spec.PostgreSQL.CredentialSecretRef.Name) != "" {
		targetMeta.Name = strings.TrimSpace(target.Spec.PostgreSQL.CredentialSecretRef.Name)
	}
	existing := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: target.Namespace, Name: targetMeta.Name}, existing); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	sourceMeta := naming.PostgreSQLCredentialSecret(source)
	if source.Spec.PostgreSQL.CredentialSecretRef != nil && strings.TrimSpace(source.Spec.PostgreSQL.CredentialSecretRef.Name) != "" {
		sourceMeta.Name = strings.TrimSpace(source.Spec.PostgreSQL.CredentialSecretRef.Name)
	}
	sourceSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: source.Namespace, Name: sourceMeta.Name}, sourceSecret); err != nil {
		return fmt.Errorf("read source PostgreSQL credential secret: %w", err)
	}
	credential := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      targetMeta.Name,
			Namespace: target.Namespace,
			Labels: naming.Merge(target.Labels, map[string]string{
				naming.LabelInstance: target.Name,
			}),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(target, v1.GroupVersion.WithKind("KDBInstance")),
			},
		},
		Type:      sourceSecret.Type,
		Immutable: sourceSecret.Immutable,
		Data:      map[string][]byte{},
	}
	for key, value := range sourceSecret.Data {
		credential.Data[key] = append([]byte(nil), value...)
	}
	if err := r.Create(ctx, credential); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *DBRestoreReconciler) cleanupCompletedRestoreBootstrap(ctx context.Context, restore *v1.DBRestore) (ctrl.Result, error) {
	targetName := strings.TrimSpace(restore.Spec.TargetInstanceName)
	if restore.Status.TargetInstanceRef != nil && strings.TrimSpace(restore.Status.TargetInstanceRef.Name) != "" {
		targetName = strings.TrimSpace(restore.Status.TargetInstanceRef.Name)
	}
	if targetName == "" {
		return ctrl.Result{}, nil
	}
	target := &v1.KDBInstance{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: restore.Namespace, Name: targetName}, target); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !restoreBootstrapCleanupRequired(target) {
		return ctrl.Result{}, nil
	}
	if err := r.cleanupRestoreBootstrap(ctx, target); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
}

func (r *DBRestoreReconciler) cleanupRestoreBootstrap(ctx context.Context, target *v1.KDBInstance) error {
	before := target.DeepCopy()
	if target.Spec.PostgreSQL != nil {
		target.Spec.PostgreSQL.Restore = nil
	}
	if target.Annotations != nil {
		delete(target.Annotations, restoreLocalRepositoryPVCAnnotation)
		if len(target.Annotations) == 0 {
			target.Annotations = nil
		}
	}
	return r.Patch(ctx, target, client.MergeFrom(before))
}

func restoreBootstrapCleanupRequired(target *v1.KDBInstance) bool {
	if target == nil {
		return false
	}
	if target.Spec.PostgreSQL != nil && target.Spec.PostgreSQL.Restore != nil {
		return true
	}
	return target.Annotations != nil && strings.TrimSpace(target.Annotations[restoreLocalRepositoryPVCAnnotation]) != ""
}

func postgresqlAvailableForCurrentGeneration(target *v1.KDBInstance) bool {
	if target == nil || target.Status.PostgreSQL == nil || !target.Status.PostgreSQL.Ready || target.Status.PostgreSQL.Primary == "" {
		return false
	}
	for i := range target.Status.Conditions {
		condition := target.Status.Conditions[i]
		if condition.Type == v1.PostgreSQLAvailable {
			return condition.Status == metav1.ConditionTrue && condition.ObservedGeneration == target.Generation
		}
	}
	return false
}

func restoredTargetInstance(source *v1.KDBInstance, backup *v1.DBBackup, restore *v1.DBRestore) *v1.KDBInstance {
	targetName := restore.Spec.TargetInstanceName
	labels := copyStringMap(source.Labels)
	labels["kdb.com/restored-from"] = backup.Name
	for _, key := range []string{"app.kubernetes.io/name", "kdb.io/instance-id"} {
		if _, exists := labels[key]; exists {
			labels[key] = targetName
		}
	}
	if _, exists := labels["kdb.io/operation-id"]; exists {
		labels["kdb.io/operation-id"] = restore.Spec.OperationID
	}
	annotations := copyStringMap(source.Annotations)
	annotations[restoreOperationAnnotation] = restore.Spec.OperationID

	spec := source.DeepCopy().Spec
	spec.Leader = v1.HostInfo{}
	running := false
	spec.Shutdown = &running
	if spec.InstanceSet.Metadata != nil {
		for _, key := range []string{"app.kubernetes.io/name", "kdb.io/instance-id"} {
			if _, exists := spec.InstanceSet.Metadata.Labels[key]; exists {
				spec.InstanceSet.Metadata.Labels[key] = targetName
			}
		}
	}
	for i := range spec.InstanceSet.TopologySpreadConstraints {
		selector := spec.InstanceSet.TopologySpreadConstraints[i].LabelSelector
		if selector == nil {
			continue
		}
		for _, key := range []string{naming.LabelInstance, "app.kubernetes.io/name", "kdb.io/instance-id"} {
			if _, exists := selector.MatchLabels[key]; exists {
				selector.MatchLabels[key] = targetName
			}
		}
	}
	if spec.PostgreSQL != nil {
		spec.PostgreSQL.CredentialSecretRef = &corev1.LocalObjectReference{Name: targetName + "-postgresql-credential"}
	}

	return &v1.KDBInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:        targetName,
			Namespace:   restore.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: spec,
	}
}

func localRepositoryPVCForBackup(backup *v1.DBBackup) (string, error) {
	executorPod := strings.TrimSpace(backup.Status.ExecutorPod)
	separator := strings.LastIndex(executorPod, "-")
	if separator <= 0 || separator == len(executorPod)-1 {
		return "", fmt.Errorf("backup %s does not identify its executor pod", backup.Name)
	}
	if _, err := strconv.ParseUint(executorPod[separator+1:], 10, 32); err != nil {
		return "", fmt.Errorf("backup %s executor pod %q has no StatefulSet ordinal", backup.Name, executorPod)
	}
	return executorPod[:separator] + "-kdb-data", nil
}

func copyStringMap(source map[string]string) map[string]string {
	target := make(map[string]string, len(source)+1)
	for key, value := range source {
		target[key] = value
	}
	return target
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
