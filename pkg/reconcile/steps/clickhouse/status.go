package clickhouse

import (
	"sort"
	"strconv"
	"time"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	clickHousePhaseCreating = "Creating"
	clickHousePhaseRunning  = "Running"
	clickHousePhaseDegraded = "Degraded"
	clickHousePhaseFailed   = "Failed"
	computeGroupActive      = "Active"
	computeGroupCreating    = "Creating"
	computeGroupDegraded    = "Degraded"
	computeGroupSuspended   = "Suspended"
)

func projectStandaloneStatus(rc *context.InstanceContext) error {
	instance := rc.GetInstance()
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		instance.Status.Phase = clickHousePhaseFailed
		metaSetClickHouseReady(instance, metav1.ConditionFalse, "SpecInvalid", err.Error())
		return nil
	}
	totalReplicas := int32(0)
	totalReady := int32(0)
	totalUpdated := int32(0)
	podInfos := []shared.PodStatusInfo{}
	groupStatuses := make([]v1.ClickHouseComputeGroupStatus, 0, len(instance.Spec.ClickHouse.ComputeGroups))
	allGroupsReady := true
	allReplicationHealthy := true
	anyRunnerObserved := false
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		groupPlans := plansForGroup(plans, group.Name)
		expectedReplicas := int32(0)
		for _, plan := range groupPlans {
			expectedReplicas += desiredHostReplicas(instance, plan.Group, plan.Replica)
		}
		totalReplicas += expectedReplicas
		readyReplicas, updatedReplicas, groupPodInfos, observed, maxDelay, readonlyReplicas, replicationHealthy, err := computeGroupKubeStatus(rc, group.Name)
		if err != nil {
			return err
		}
		anyRunnerObserved = anyRunnerObserved || observed
		totalReady += readyReplicas
		totalUpdated += updatedReplicas
		podInfos = append(podInfos, groupPodInfos...)
		groupPhase := computeGroupCreating
		if group.Role == v1.ClickHouseRoleAdhoc && shouldSuspendGroup(instance, group, time.Now()) && readyReplicas == expectedReplicas {
			groupPhase = computeGroupSuspended
		} else if observed && readyReplicas == expectedReplicas {
			groupPhase = computeGroupActive
		} else if observed && readyReplicas == 0 {
			groupPhase = computeGroupDegraded
		}
		if groupPhase != computeGroupActive && groupPhase != computeGroupSuspended {
			allGroupsReady = false
		}
		allReplicationHealthy = allReplicationHealthy && replicationHealthy
		groupStatuses = append(groupStatuses, v1.ClickHouseComputeGroupStatus{
			Name:             group.Name,
			Role:             group.Role,
			Phase:            groupPhase,
			ReplicasPerShard: replicasPerShard(group),
			ReadyReplicas:    readyReplicas,
				UpdatedReplicas:  updatedReplicas,
				MaxReplicationDelaySeconds: maxDelay,
				ReadonlyReplicas: readonlyReplicas,
		})
	}
	instance.Status.InstanceSet = shared.InstanceSetStatus{
		Replicas:        totalReplicas,
		ReadyReplicas:   totalReady,
		UpdatedReplicas: totalUpdated,
		PodInfos:        podInfos,
	}
	instancePhase := clickHousePhaseCreating
	conditionStatus := metav1.ConditionFalse
	conditionReason := "ReplicaStartupPending"
	if anyRunnerObserved && allGroupsReady {
		instancePhase = clickHousePhaseRunning
		conditionStatus = metav1.ConditionTrue
		conditionReason = "AllRequiredGroupsReady"
	} else if anyRunnerObserved && totalReady == 0 {
		instancePhase = clickHousePhaseDegraded
		conditionReason = "RequiredGroupUnavailable"
	}
	instance.Status.Phase = instancePhase
	previousClickHouse := instance.Status.ClickHouse
	minVersion, maxVersion, versionErr := observedClickHouseVersions(rc)
	if versionErr != nil {
		return versionErr
	}
	instance.Status.ClickHouse = &v1.ClickHouseStatus{
		DataShards:    instance.Spec.ClickHouse.DataShards,
		ComputeGroups: groupStatuses,
		Version: &v1.ClickHouseVersionStatus{Min: minVersion, Max: maxVersion},
	}
	if previousClickHouse != nil {
		instance.Status.ClickHouse.Keeper = previousClickHouse.Keeper
		instance.Status.ClickHouse.Gateway = previousClickHouse.Gateway
	}
	metaSetClickHouseReady(instance, conditionStatus, conditionReason, "ClickHouse compute group status projected")
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type: "ComputeGroupsReady", Status: conditionStatus, Reason: conditionReason,
		Message: "ClickHouse compute group readiness projected", ObservedGeneration: instance.Generation,
	})
	replicationCondition := metav1.ConditionTrue
	replicationReason := "ReplicationHealthy"
	if !allReplicationHealthy {
		replicationCondition = metav1.ConditionFalse
		replicationReason = "ReplicaHealthUnavailable"
	}
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type: "ReplicationHealthy", Status: replicationCondition, Reason: replicationReason,
		Message: "ClickHouse sidecar replication health projected", ObservedGeneration: instance.Generation,
	})
	backupStatus := metav1.ConditionFalse
	backupReason := "BackupDisabled"
	backupMessage := "ClickHouse backup runner is not configured"
	if clickHouseBackupRunnerEnabled(instance) && instance.Spec.ClickHouse.Backup != nil && instance.Spec.ClickHouse.Backup.ObjectStorageRef != nil {
		backupStatus = metav1.ConditionTrue
		backupReason = "BackupConfigured"
		backupMessage = "ClickHouse backup runner and object storage reference are configured"
	}
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type: "BackupConfigured", Status: backupStatus, Reason: backupReason,
		Message: backupMessage, ObservedGeneration: instance.Generation,
	})
	return nil
}

func observedClickHouseVersions(rc *context.InstanceContext) (string, string, error) {
	pods := &corev1.PodList{}
	selector, err := naming.AsSelector(metav1.LabelSelector{MatchLabels: naming.ClickHouseLabels(rc.GetInstance().Name, naming.ClickHouseComponentClickHouse)})
	if err != nil {
		return "", "", err
	}
	if err = rc.List(pods, selector); err != nil {
		return "", "", err
	}
	versions := []string{}
	for i := range pods.Items {
		if version := pods.Items[i].Annotations[annotationEngineVersion]; version != "" {
			versions = append(versions, version)
		}
	}
	if len(versions) == 0 {
		version := rc.GetInstance().Spec.EngineVersion
		return version, version, nil
	}
	sort.Strings(versions)
	return versions[0], versions[len(versions)-1], nil
}

func computeGroupKubeStatus(rc *context.InstanceContext, group string) (int32, int32, []shared.PodStatusInfo, bool, int64, int32, bool, error) {
	pods := &corev1.PodList{}
	runners := &appsv1.StatefulSetList{}
	selector, err := naming.AsSelector(metav1.LabelSelector{MatchLabels: map[string]string{
		naming.LabelInstance:               rc.GetInstance().Name,
		naming.LabelClickHouseEngine:       naming.ClickHouseEngine,
		naming.LabelClickHouseComponent:    naming.ClickHouseComponentClickHouse,
		naming.LabelClickHouseComputeGroup: group,
	}})
	if err != nil {
		return 0, 0, nil, false, 0, 0, false, err
	}
	if err = rc.List(pods, selector); err != nil {
		return 0, 0, nil, false, 0, 0, false, err
	}
	if err = rc.List(runners, selector); err != nil {
		return 0, 0, nil, false, 0, 0, false, err
	}
	readyReplicas := int32(0)
	updatedReplicas := int32(0)
	podInfos := make([]shared.PodStatusInfo, 0, len(pods.Items))
	maxDelay := int64(0)
	readonlyReplicas := int32(0)
	replicationHealthy := true
	maxRoutableDelay := maxRoutableReplicationDelay(rc.GetInstance())
	for i := range pods.Items {
		pod := &pods.Items[i]
		routable := false
		if util.IsPodReady(pod) {
			status, statusErr := queryClickHouseSidecarStatus(rc.Context(), pod.Status.PodIP)
			if statusErr == nil && status.Healthy {
				routable = replicaRoutable(status, maxRoutableDelay)
				if status.Readonly {
					readonlyReplicas++
				}
				if status.ReplicationDelaySeconds > maxDelay {
					maxDelay = status.ReplicationDelaySeconds
				}
			} else {
				replicationHealthy = false
			}
		}
		if err = setPodRoutableLabel(rc, pod, routable); err != nil {
			return 0, 0, nil, false, 0, 0, false, err
		}
		if routable {
			readyReplicas++
			podInfos = append(podInfos, shared.PodStatusInfo{
				PodName:  pod.Name,
				PodPhase: pod.Status.Phase,
				PodIP:    pod.Status.PodIP,
				NodeName: pod.Spec.NodeName,
				HostIP:   pod.Status.HostIP,
			})
		}
	}
	for i := range runners.Items {
		updatedReplicas += runners.Items[i].Status.UpdatedReplicas
	}
	return readyReplicas, updatedReplicas, podInfos, len(runners.Items) > 0, maxDelay, readonlyReplicas, replicationHealthy, nil
}

func maxRoutableReplicationDelay(instance *v1.KDBInstance) int64 {
	if instance != nil && instance.Spec.Config != nil {
		if value, err := strconv.ParseInt(instance.Spec.Config["clickhouse.maxReplicationDelaySeconds"], 10, 64); err == nil && value >= 0 {
			return value
		}
	}
	return 60
}

func setPodRoutableLabel(rc *context.InstanceContext, pod *corev1.Pod, routable bool) error {
	value := strconv.FormatBool(routable)
	if pod.Labels != nil && pod.Labels[naming.LabelClickHouseRoutable] == value {
		return nil
	}
	before := pod.DeepCopy()
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	pod.Labels[naming.LabelClickHouseRoutable] = value
	return rc.Client().Patch(rc.Context(), pod, client.MergeFrom(before))
}

func metaSetClickHouseReady(instance *v1.KDBInstance, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: instance.Generation,
	})
}
