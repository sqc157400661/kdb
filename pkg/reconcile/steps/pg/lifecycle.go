package pg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/postgresqllifecycle"
	"github.com/sqc157400661/kdb/internal/postgresqlruntime"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	lifecyclePhasePreflight          = "Preflight"
	lifecyclePhaseBlocked            = "Blocked"
	lifecyclePhaseRolling            = "Rolling"
	lifecyclePhaseStopping           = "Stopping"
	lifecyclePhaseUpgrading          = "Upgrading"
	lifecyclePhaseRebuildingReplicas = "RebuildingReplicas"
	lifecyclePhaseSucceeded          = "Succeeded"
	lifecyclePhaseFailed             = "Failed"
)

func (s *InstanceStepManager) ReconcileLifecycle() kube.BindFunc {
	return s.StepBinder("ReconcilePostgreSQLLifecycle", func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
		instance := rc.GetInstance()
		if err := postgresqllifecycle.ValidateUpdate(nil, instance); err != nil {
			return flow.Error(err, "validate PostgreSQL lifecycle request err")
		}
		if instance.Spec.PostgreSQL == nil || instance.Spec.PostgreSQL.Lifecycle == nil {
			return flow.Pass()
		}
		if err := reconcilePostgreSQLExtensions(rc, instance); err != nil {
			return flow.Error(err, "reconcile controlled PostgreSQL extensions err")
		}
		if instance.Spec.PostgreSQL.Lifecycle.Rebuild != nil {
			return reconcilePostgreSQLReplicaRebuild(rc, flow, instance, instance.Spec.PostgreSQL.Lifecycle.Rebuild)
		}
		if instance.Spec.PostgreSQL.Lifecycle.Upgrade == nil {
			return flow.Pass()
		}
		upgrade := instance.Spec.PostgreSQL.Lifecycle.Upgrade
		if upgrade.Type == "Minor" {
			return reconcilePostgreSQLMinorUpgrade(rc, flow, instance, upgrade)
		}
		return reconcilePostgreSQLMajorUpgrade(rc, flow, instance, upgrade)
	})
}

func reconcilePostgreSQLExtensions(rc *context.InstanceContext, instance *v1.KDBInstance) error {
	if instance.Status.PostgreSQL == nil || !instance.Status.PostgreSQL.Ready {
		return nil
	}
	lease := &coordinationv1.Lease{}
	if err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: naming.PostgreSQLLeaderLeaseName(instance.Name)}, lease); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if postgresqlLeaseExpired(lease, time.Now()) || lease.Spec.HolderIdentity == nil {
		return nil
	}
	primary, _ := postgreSQLHolderIdentity(*lease.Spec.HolderIdentity)
	if primary == "" {
		return nil
	}
	for _, extension := range instance.Spec.PostgreSQL.Lifecycle.Extensions {
		name := strings.ToLower(strings.TrimSpace(extension.Name))
		if name == "" || name == "contrib" {
			continue
		}
		if name == "pgvector" {
			name = "vector"
		}
		payload, err := json.Marshal(map[string]interface{}{"action": "create", "name": name, "database": "postgres", "ifNotExists": true})
		if err != nil {
			return err
		}
		httpClient, username, password, err := postgresqlruntime.Client(rc.Context(), rc.Client(), instance, 20*time.Second)
		if err != nil {
			return err
		}
		endpoint := postgresqlruntime.PodEndpoint(instance, primary) + "/v1/postgresql/management/extensions"
		request, err := http.NewRequestWithContext(rc.Context(), http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		request.SetBasicAuth(username, password)
		response, err := httpClient.Do(request)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("create PostgreSQL extension %s returned HTTP %d", name, response.StatusCode)
		}
	}
	return nil
}

func reconcilePostgreSQLReplicaRebuild(rc *context.InstanceContext, flow kube.Flow, instance *v1.KDBInstance, rebuild *v1.PostgreSQLRebuildSpec) (reconcile.Result, error) {
	if rebuild.OperationID == "" || rebuild.PodName == "" || !rebuild.AbandonOldVolume {
		return flow.Error(fmt.Errorf("replica rebuild requires operationId, podName and abandonOldVolume=true"), "validate PostgreSQL replica rebuild err")
	}
	if instance.Status.PostgreSQL == nil || rebuild.PodName == instance.Status.PostgreSQL.Primary {
		return flow.Error(fmt.Errorf("primary member cannot be rebuilt through the replica workflow"), "validate PostgreSQL replica rebuild target err")
	}
	readyReplica := false
	for _, member := range instance.Status.PostgreSQL.Members {
		if member.Name == rebuild.PodName && member.Role == "replica" {
			readyReplica = true
		}
	}
	status := instance.Status.PostgreSQL.Lifecycle
	if status == nil || status.OperationID != rebuild.OperationID {
		if !readyReplica {
			return flow.Error(fmt.Errorf("rebuild target must be an observed replica"), "validate PostgreSQL replica rebuild member err")
		}
		if rebuild.Source == "Backup" {
			backup := &v1.DBBackup{}
			if err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: rebuild.BackupRef}, backup); err != nil || backup.Status.Phase != v1.DBBackupPhaseSucceeded {
				return flow.Error(fmt.Errorf("successful backupRef is required for backup rebuild"), "validate PostgreSQL replica rebuild backup err")
			}
		}
		pod := &corev1.Pod{}
		if err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: rebuild.PodName}, pod); err != nil {
			return flow.Error(err, "get PostgreSQL rebuild Pod err")
		}
		setName := ""
		for _, owner := range pod.OwnerReferences {
			if owner.Kind == "StatefulSet" {
				setName = owner.Name
			}
		}
		if setName == "" {
			return flow.Error(fmt.Errorf("rebuild Pod has no StatefulSet owner"), "resolve PostgreSQL rebuild owner err")
		}
		pvc := &corev1.PersistentVolumeClaim{}
		pvcName := setName + "-kdb-data"
		if err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: pvcName}, pvc); err != nil {
			return flow.Error(err, "get PostgreSQL rebuild PVC err")
		}
		now := metav1.Now()
		status = &v1.PostgreSQLLifecycleStatus{OperationID: rebuild.OperationID, Kind: "ReplicaRebuild", Phase: lifecyclePhaseStopping, MemberName: rebuild.PodName, JobName: setName, OldPVCUID: string(pvc.UID), PrimaryBefore: instance.Status.PostgreSQL.Primary, StartedAt: &now, Message: "deleting lost replica and abandoning its old PVC"}
		instance.Status.PostgreSQL.Lifecycle = status
	}

	pvc := &corev1.PersistentVolumeClaim{}
	pvcKey := client.ObjectKey{Namespace: instance.Namespace, Name: status.JobName + "-kdb-data"}
	if status.Phase != lifecyclePhaseRolling {
		set := &appsv1.StatefulSet{}
		setKey := client.ObjectKey{Namespace: instance.Namespace, Name: status.JobName}
		if err := rc.Client().Get(rc.Context(), setKey, set); err == nil {
			if err := rc.Client().Delete(rc.Context(), set); err != nil && !apierrors.IsNotFound(err) {
				return flow.Error(err, "delete PostgreSQL rebuild StatefulSet err")
			}
			return flow.RetryAfter(2*time.Second, "waiting for rebuild StatefulSet deletion")
		} else if !apierrors.IsNotFound(err) {
			return flow.Error(err, "get PostgreSQL rebuild StatefulSet err")
		}
		if err := rc.Client().Get(rc.Context(), pvcKey, pvc); err == nil {
			if err := rc.Client().Delete(rc.Context(), pvc); err != nil && !apierrors.IsNotFound(err) {
				return flow.Error(err, "delete PostgreSQL rebuild old PVC err")
			}
			return flow.RetryAfter(2*time.Second, "waiting for old PostgreSQL replica PVC deletion")
		} else if !apierrors.IsNotFound(err) {
			return flow.Error(err, "get PostgreSQL rebuild old PVC err")
		}
		status.Phase, status.Message = lifecyclePhaseRolling, "creating a new PVC and cloning replica from the current primary"
		return flow.Pass()
	}
	if err := rc.Client().Get(rc.Context(), pvcKey, pvc); err == nil && string(pvc.UID) != status.OldPVCUID {
		pod := &corev1.Pod{}
		if err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: status.MemberName}, pod); err == nil && podReady(pod) {
			now := metav1.Now()
			status.Phase, status.NewPVCUID, status.PrimaryAfter, status.Message, status.CompletedAt = lifecyclePhaseSucceeded, string(pvc.UID), instance.Status.PostgreSQL.Primary, "replica rebuilt on a newly provisioned volume", &now
			return flow.Pass()
		}
	}
	return flow.Pass()
}

func lifecycleStatus(instance *v1.KDBInstance, upgrade *v1.PostgreSQLUpgradeSpec) *v1.PostgreSQLLifecycleStatus {
	if instance.Status.PostgreSQL == nil {
		instance.Status.PostgreSQL = &v1.PostgreSQLStatus{}
	}
	status := instance.Status.PostgreSQL.Lifecycle
	if status == nil || status.OperationID != upgrade.OperationID {
		now := metav1.Now()
		status = &v1.PostgreSQLLifecycleStatus{OperationID: upgrade.OperationID, Kind: upgrade.Type + "Upgrade", Phase: lifecyclePhasePreflight, CurrentVersion: instance.Spec.EngineFullVersion, TargetVersion: upgrade.TargetFullVersion, Irreversible: upgrade.Type == "Major", StartedAt: &now}
		if status.CurrentVersion == "" {
			status.CurrentVersion = instance.Spec.EngineVersion
		}
		if status.TargetVersion == "" {
			status.TargetVersion = upgrade.TargetMajorVersion
		}
		if instance.Status.PostgreSQL != nil {
			status.PrimaryBefore = instance.Status.PostgreSQL.Primary
		}
		instance.Status.PostgreSQL.Lifecycle = status
	}
	return status
}

func reconcilePostgreSQLMinorUpgrade(rc *context.InstanceContext, flow kube.Flow, instance *v1.KDBInstance, upgrade *v1.PostgreSQLUpgradeSpec) (reconcile.Result, error) {
	status := lifecycleStatus(instance, upgrade)
	changed, err := persistPostgreSQLUpgradeTarget(rc, instance, upgrade, false)
	if err != nil {
		return flow.Error(err, "persist PostgreSQL minor upgrade target err")
	}
	if changed {
		status.Phase, status.Message, status.CompletedAt = lifecyclePhaseRolling, "persisted minor target; replica-first rolling upgrade is in progress", nil
		return flow.RetryAfter(time.Second, "PostgreSQL minor target persisted")
	}
	if status.Phase == lifecyclePhaseSucceeded {
		return flow.Pass()
	}
	splitRuntime := naming.PostgreSQLSplitRuntime(instance)
	instance.Spec.InstanceSet.MainContainer.Image = upgrade.TargetImage
	if !splitRuntime {
		instance.Spec.InstanceSet.SidecarContainer.Image = upgrade.TargetImage
	}
	if upgrade.TargetFullVersion != "" {
		instance.Spec.EngineFullVersion = upgrade.TargetFullVersion
	}
	status.Phase = lifecyclePhaseRolling
	status.Message = "replica-first rolling upgrade is in progress"

	pods := &corev1.PodList{}
	if err := rc.Client().List(rc.Context(), pods, client.InNamespace(instance.Namespace), client.MatchingLabels{naming.LabelInstance: instance.Name}); err != nil {
		return flow.Error(err, "list PostgreSQL lifecycle pods err")
	}
	desired := int32(0)
	if instance.Spec.InstanceSet.Replicas != nil {
		desired = *instance.Spec.InstanceSet.Replicas
	}
	readyTarget := int32(0)
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !podOwnedByStatefulSet(pod) || !podReady(pod) {
			continue
		}
		for _, container := range pod.Spec.Containers {
			if container.Name == naming.ContainerDatabase && container.Image == upgrade.TargetImage {
				readyTarget++
			}
		}
	}
	if desired > 0 && readyTarget == desired && instance.Status.PostgreSQL != nil && instance.Status.PostgreSQL.Ready {
		now := metav1.Now()
		status.Phase, status.Message, status.CompletedAt = lifecyclePhaseSucceeded, "minor upgrade completed", &now
		status.PrimaryAfter = instance.Status.PostgreSQL.Primary
		return flow.Pass()
	}
	return flow.Pass()
}

func reconcilePostgreSQLMajorUpgrade(rc *context.InstanceContext, flow kube.Flow, instance *v1.KDBInstance, upgrade *v1.PostgreSQLUpgradeSpec) (reconcile.Result, error) {
	status := lifecycleStatus(instance, upgrade)
	if status.Phase == lifecyclePhaseSucceeded {
		return flow.Pass()
	}
	// Persisting the target version is the durable boundary between destructive
	// replica-volume cleanup and replica reconstruction. Continue from that
	// boundary even if the completed Job was garbage-collected or the Operator
	// restarted; recreating the Job would attempt pg_upgrade a second time.
	if instance.Spec.EngineVersion == upgrade.TargetMajorVersion && status.MemberName != "" {
		return resumePostgreSQLAfterMajorUpgrade(rc, flow, instance, upgrade, status, status.MemberName)
	}
	backup := &v1.DBBackup{}
	if err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: upgrade.RecoverableBackupRef}, backup); err != nil || backup.Status.Phase != v1.DBBackupPhaseSucceeded || backup.Status.Artifact == nil {
		status.Phase = lifecyclePhaseBlocked
		status.Message = "recoverable succeeded backup is required before major upgrade"
		return flow.Pass()
	}
	if instance.Spec.InstanceSet.LogVolumeClaimSpec != nil {
		status.Phase = lifecyclePhaseBlocked
		status.Message = "major upgrade with a separate WAL PVC requires an explicit disk migration plan"
		return flow.Pass()
	}
	jobName := majorUpgradeJobName(instance.Name, upgrade.OperationID)
	job := &batchv1.Job{}
	jobErr := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: jobName}, job)
	if jobErr == nil {
		status.JobName = jobName
		if job.Status.Failed > 0 {
			status.Phase, status.Message = lifecyclePhaseFailed, "pg_upgrade Job failed; automatic downgrade is not supported"
			return flow.Pass()
		}
		if job.Status.Succeeded == 0 {
			status.Phase, status.Message = lifecyclePhaseUpgrading, "pg_upgrade Job is running"
			return flow.RetryAfter(3*time.Second, "waiting for PostgreSQL major upgrade Job")
		}
		if status.MemberName == "" {
			status.Phase, status.Message = lifecyclePhaseFailed, "upgraded primary StatefulSet was not recorded"
			return flow.Pass()
		}
		return resumePostgreSQLAfterMajorUpgrade(rc, flow, instance, upgrade, status, status.MemberName)
	}
	if !apierrors.IsNotFound(jobErr) {
		return flow.Error(jobErr, "get major upgrade Job err")
	}
	primarySet := status.MemberName
	if primarySet == "" {
		var err error
		primarySet, err = primaryStatefulSetName(rc, instance)
		if err != nil {
			status.Phase, status.Message = lifecyclePhaseBlocked, err.Error()
			return flow.Pass()
		}
		status.MemberName = primarySet
	}
	status.Phase = lifecyclePhaseStopping
	status.Message = "maintenance window active; stopping PostgreSQL members"
	stopped, err := scalePostgreSQLStatefulSetsToZero(rc, instance)
	if err != nil {
		return flow.Error(err, "stop PostgreSQL for major upgrade err")
	}
	if !stopped {
		return flow.RetryAfter(3*time.Second, "waiting for PostgreSQL members to stop for major upgrade")
	}

	job = majorUpgradeJob(instance, upgrade, primarySet, jobName)
	if err := rc.SetControllerReference(job); err != nil {
		return flow.Error(err, "set major upgrade Job owner err")
	}
	if err := rc.Client().Create(rc.Context(), job); err != nil {
		return flow.Error(err, "create major upgrade Job err")
	}
	status.Phase, status.JobName, status.Message = lifecyclePhaseUpgrading, jobName, "pg_upgrade --check passed; upgrade Job is running"
	return flow.RetryAfter(3*time.Second, "PostgreSQL major upgrade Job created")
}

func resumePostgreSQLAfterMajorUpgrade(rc *context.InstanceContext, flow kube.Flow, instance *v1.KDBInstance, upgrade *v1.PostgreSQLUpgradeSpec, status *v1.PostgreSQLLifecycleStatus, primarySet string) (reconcile.Result, error) {
	cleaned, err := removeStaleMajorUpgradeJobs(rc, instance, status.JobName)
	if err != nil {
		return flow.Error(err, "remove stale PostgreSQL major upgrade Jobs err")
	}
	if cleaned {
		status.Phase, status.Message = lifecyclePhaseRebuildingReplicas, "stale upgrade Jobs removed; releasing old replica volumes"
		return flow.RetryAfter(time.Second, "waiting for stale PostgreSQL major upgrade Jobs to be deleted")
	}
	if instance.Spec.EngineVersion != upgrade.TargetMajorVersion {
		removed, err := removeMajorUpgradeReplicaVolumes(rc, instance, primarySet)
		if err != nil {
			return flow.Error(err, "remove old replica volumes after major upgrade err")
		}
		if removed {
			status.Phase, status.Message = lifecyclePhaseRebuildingReplicas, "old replica volumes removed; replicas will clone from upgraded primary"
			return flow.RetryAfter(3*time.Second, "waiting for old replica volumes to be deleted")
		}
	}
	changed, err := persistPostgreSQLUpgradeTarget(rc, instance, upgrade, true)
	if err != nil {
		return flow.Error(err, "persist PostgreSQL major upgrade target err")
	}
	if changed {
		status.Phase, status.Message = lifecyclePhaseRebuildingReplicas, "persisted major target; starting upgraded primary and rebuilding replicas"
		return flow.RetryAfter(time.Second, "PostgreSQL major target persisted")
	}
	status.Phase, status.Message = lifecyclePhaseRebuildingReplicas, "starting upgraded primary and rebuilding replicas from it"

	pods := &corev1.PodList{}
	if err := rc.Client().List(rc.Context(), pods, client.InNamespace(instance.Namespace), client.MatchingLabels{naming.LabelInstance: instance.Name}); err != nil {
		return flow.Error(err, "list PostgreSQL pods after major upgrade err")
	}
	desired := int32(0)
	if instance.Spec.InstanceSet.Replicas != nil {
		desired = *instance.Spec.InstanceSet.Replicas
	}
	readyTarget := int32(0)
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !podOwnedByStatefulSet(pod) || !podReady(pod) {
			continue
		}
		for _, container := range pod.Spec.Containers {
			if container.Name == naming.ContainerDatabase && container.Image == upgrade.TargetImage {
				readyTarget++
			}
		}
	}
	if desired > 0 && readyTarget == desired && instance.Status.PostgreSQL != nil && instance.Status.PostgreSQL.Ready {
		now := metav1.Now()
		status.Phase, status.Message, status.CompletedAt = lifecyclePhaseSucceeded, "major upgrade completed; downgrade requires restore to a new instance", &now
		status.PrimaryAfter = instance.Status.PostgreSQL.Primary
	}
	return flow.Pass()
}

// removeStaleMajorUpgradeJobs releases PVC references held by terminal Jobs
// from previous operation IDs. The successful Job for the current operation is
// retained until the upgraded primary has restarted, otherwise a subsequent
// reconcile could recreate it and run pg_upgrade twice.
func removeStaleMajorUpgradeJobs(rc *context.InstanceContext, instance *v1.KDBInstance, currentJobName string) (bool, error) {
	jobs := &batchv1.JobList{}
	if err := rc.Client().List(rc.Context(), jobs,
		client.InNamespace(instance.Namespace),
		client.MatchingLabels{
			naming.LabelInstance:           instance.Name,
			"postgresql.kdb.com/lifecycle": "major-upgrade",
		}); err != nil {
		return false, err
	}
	removed := false
	propagation := metav1.DeletePropagationBackground
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if job.Name == currentJobName {
			continue
		}
		if err := rc.Client().Delete(rc.Context(), job, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !apierrors.IsNotFound(err) {
			return false, errors.WithStack(err)
		}
		removed = true
	}
	return removed, nil
}

func persistPostgreSQLUpgradeTarget(rc *context.InstanceContext, instance *v1.KDBInstance, upgrade *v1.PostgreSQLUpgradeSpec, major bool) (bool, error) {
	targetFullVersion := upgrade.TargetFullVersion
	if targetFullVersion == "" && major {
		targetFullVersion = upgrade.TargetMajorVersion
	}
	sidecarReady := naming.PostgreSQLSplitRuntime(instance) || instance.Spec.InstanceSet.SidecarContainer.Image == upgrade.TargetImage
	if instance.Spec.InstanceSet.MainContainer.Image == upgrade.TargetImage && sidecarReady &&
		(targetFullVersion == "" || instance.Spec.EngineFullVersion == targetFullVersion) &&
		(!major || instance.Spec.EngineVersion == upgrade.TargetMajorVersion) {
		return false, nil
	}
	latest := &v1.KDBInstance{}
	key := client.ObjectKeyFromObject(instance)
	if err := rc.Client().Get(rc.Context(), key, latest); err != nil {
		return false, err
	}
	before := latest.DeepCopy()
	latest.Spec.InstanceSet.MainContainer.Image = upgrade.TargetImage
	if !naming.PostgreSQLSplitRuntime(instance) {
		latest.Spec.InstanceSet.SidecarContainer.Image = upgrade.TargetImage
	}
	if targetFullVersion != "" {
		latest.Spec.EngineFullVersion = targetFullVersion
	}
	if major {
		latest.Spec.EngineVersion = upgrade.TargetMajorVersion
	}
	if err := rc.Client().Patch(rc.Context(), latest, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return false, err
	}
	return true, nil
}

func primaryStatefulSetName(rc *context.InstanceContext, instance *v1.KDBInstance) (string, error) {
	if instance.Status.PostgreSQL == nil || instance.Status.PostgreSQL.Primary == "" {
		return "", fmt.Errorf("validated primary is required before major upgrade")
	}
	pod := &corev1.Pod{}
	if err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: instance.Status.PostgreSQL.Primary}, pod); err != nil {
		return "", err
	}
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "StatefulSet" {
			return owner.Name, nil
		}
	}
	return "", fmt.Errorf("primary Pod has no StatefulSet owner")
}

func scalePostgreSQLStatefulSetsToZero(rc *context.InstanceContext, instance *v1.KDBInstance) (bool, error) {
	sets := &appsv1.StatefulSetList{}
	if err := rc.Client().List(rc.Context(), sets, client.InNamespace(instance.Namespace), client.MatchingLabels{naming.LabelInstance: instance.Name}); err != nil {
		return false, err
	}
	changed := false
	zero := int32(0)
	for i := range sets.Items {
		if sets.Items[i].Spec.Replicas == nil || *sets.Items[i].Spec.Replicas != 0 {
			sets.Items[i].Spec.Replicas = &zero
			if err := rc.Client().Update(rc.Context(), &sets.Items[i]); err != nil {
				return false, err
			}
			changed = true
		}
	}
	pods := &corev1.PodList{}
	if err := rc.Client().List(rc.Context(), pods, client.InNamespace(instance.Namespace), client.MatchingLabels{naming.LabelInstance: instance.Name}); err != nil {
		return false, err
	}
	for i := range pods.Items {
		if podOwnedByStatefulSet(&pods.Items[i]) {
			return false, nil
		}
	}
	return !changed, nil
}

func majorUpgradeJob(instance *v1.KDBInstance, upgrade *v1.PostgreSQLUpgradeSpec, primarySet, name string) *batchv1.Job {
	backoff := int32(0)
	fsGroup := int64(102)
	deadline := int64(3600)
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: instance.Namespace, Labels: map[string]string{naming.LabelInstance: instance.Name, "postgresql.kdb.com/lifecycle": "major-upgrade"}}, Spec: batchv1.JobSpec{BackoffLimit: &backoff, ActiveDeadlineSeconds: &deadline, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{naming.LabelInstance: instance.Name}}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, SecurityContext: &corev1.PodSecurityContext{FSGroup: &fsGroup}, Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: primarySet + "-kdb-data"}}}, {Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}, Containers: []corev1.Container{{Name: "pg-upgrade", Image: upgrade.TargetImage, Command: []string{"/kdb/bin/postgres-major-upgrade.sh"}, Args: []string{instance.Spec.EngineVersion, upgrade.TargetMajorVersion, "/pgdata/pg" + instance.Spec.EngineVersion, "/pgdata/pg" + upgrade.TargetMajorVersion}, VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/pgdata"}, {Name: "tmp", MountPath: "/tmp"}}}}}}}}
}

func removeMajorUpgradeReplicaVolumes(rc *context.InstanceContext, instance *v1.KDBInstance, primarySet string) (bool, error) {
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := rc.Client().List(rc.Context(), pvcs, client.InNamespace(instance.Namespace), client.MatchingLabels{naming.LabelInstance: instance.Name}); err != nil {
		return false, err
	}
	removed := false
	for i := range pvcs.Items {
		pvc := &pvcs.Items[i]
		if pvc.Name == primarySet+"-kdb-data" || !strings.HasSuffix(pvc.Name, "-kdb-data") {
			continue
		}
		if err := rc.Client().Delete(rc.Context(), pvc); err != nil && !apierrors.IsNotFound(err) {
			return false, errors.WithStack(err)
		}
		removed = true
	}
	return removed, nil
}

func majorUpgradeJobName(instanceName, operationID string) string {
	value := strings.ToLower(instanceName + "-major-" + operationID)
	value = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, value)
	value = strings.Trim(value, "-")
	if len(value) > 63 {
		value = strings.TrimRight(value[:63], "-")
	}
	return value
}

func podOwnedByStatefulSet(pod *corev1.Pod) bool {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "StatefulSet" {
			return true
		}
	}
	return false
}
