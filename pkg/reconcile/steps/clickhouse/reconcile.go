package clickhouse

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	annotationEngineVersion   = "clickhouse.kdb.com/engine-version"
	annotationRolloutDraining = "clickhouse.kdb.com/rollout-draining"
	annotationBackupConfigRevision = "clickhouse.kdb.com/backup-config-revision"
	annotationCredentialRevision = "clickhouse.kdb.com/credential-revision"
)

func reconcileClickHouseScaleDownAndRolling(rc *context.InstanceContext) (bool, error) {
	if err := deleteObsoleteClickHouseResources(rc); err != nil {
		return false, err
	}
	return reconcileNextClickHousePodRollout(rc)
}

func clickHouseBackupConfigRevision(rc *context.InstanceContext) (string, error) {
	instance := rc.GetInstance()
	if !clickHouseBackupRunnerEnabled(instance) || instance.Spec.ClickHouse.Backup.ObjectStorageRef == nil {
		return "", nil
	}
	return clickHouseSecretRevision(rc, instance.Spec.ClickHouse.Backup.ObjectStorageRef.Name)
}

func clickHouseCredentialRevision(rc *context.InstanceContext) (string, error) {
	return clickHouseSecretRevision(rc, naming.ClickHouseSecretName(rc.GetInstance().Name))
}

func clickHouseSecretRevision(rc *context.InstanceContext, name string) (string, error) {
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: rc.GetInstance().Namespace, Name: name}
	if err := rc.Client().Get(rc.Context(), key, secret); err != nil {
		return "", err
	}
	keys := make([]string, 0, len(secret.Data))
	for key := range secret.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := []string{secret.Name}
	for _, key := range keys {
		parts = append(parts, key, string(secret.Data[key]))
	}
	return revisionHash(parts...), nil
}

func validateObservedClickHouseChanges(rc *context.InstanceContext) error {
	instance := rc.GetInstance()
	selector, err := naming.AsSelector(metav1.LabelSelector{MatchLabels: map[string]string{
		naming.LabelInstance: instance.Name,
		naming.LabelClickHouseEngine: naming.ClickHouseEngine,
		naming.LabelClickHouseComponent: naming.ClickHouseComponentClickHouse,
	}})
	if err != nil {
		return err
	}
	statefulSets := &appsv1.StatefulSetList{}
	if err = rc.List(statefulSets, selector); err != nil {
		return err
	}
	observedReplicas := map[string]map[string]struct{}{}
	for i := range statefulSets.Items {
		group := statefulSets.Items[i].Labels[naming.LabelClickHouseComputeGroup]
		replica := statefulSets.Items[i].Labels[naming.LabelClickHouseReplica]
		if group == "" || replica == "" {
			continue
		}
		if observedReplicas[group] == nil {
			observedReplicas[group] = map[string]struct{}{}
		}
		observedReplicas[group][replica] = struct{}{}
	}
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		oldReplicas := int32(len(observedReplicas[group.Name]))
		newReplicas := replicasPerShard(group)
		if oldReplicas > 0 && oldReplicas != newReplicas && !replicaChangeConfirmed(instance, group.Name, oldReplicas, newReplicas) {
			return fmt.Errorf("clickhouse replicasPerShard change for %s requires confirmation annotation %s=%s:%d->%d", group.Name, annotationGroupReplicaChangeConfirmed, group.Name, oldReplicas, newReplicas)
		}
	}
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err = rc.List(pvcs, selector); err != nil {
		return err
	}
	desiredGroups := map[string]v1.ClickHouseComputeGroupSpec{}
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		desiredGroups[group.Name] = group
	}
	for i := range pvcs.Items {
		pvc := &pvcs.Items[i]
		group, ok := desiredGroups[pvc.Labels[naming.LabelClickHouseComputeGroup]]
		if !ok {
			continue
		}
		desired := group.Instance.DataVolumeClaimSpec
		observedSize := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		if !observedSize.IsZero() && !desired.Size.IsZero() && desired.Size.Cmp(observedSize) < 0 {
			return fmt.Errorf("clickhouse compute group %s pvc size must not shrink", group.Name)
		}
		if pvc.Spec.StorageClassName != nil && desired.StorageClass != "" && *pvc.Spec.StorageClassName != desired.StorageClass {
			return fmt.Errorf("clickhouse compute group %s storageClass is immutable", group.Name)
		}
	}
	return nil
}

func reconcileClickHousePVCExpansion(rc *context.InstanceContext) error {
	instance := rc.GetInstance()
	selector, err := naming.AsSelector(metav1.LabelSelector{MatchLabels: map[string]string{
		naming.LabelInstance: instance.Name,
		naming.LabelClickHouseEngine: naming.ClickHouseEngine,
	}})
	if err != nil {
		return err
	}
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err = rc.List(pvcs, selector); err != nil {
		return err
	}
	desiredByGroup := map[string]resource.Quantity{}
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		desiredByGroup[group.Name] = group.Instance.DataVolumeClaimSpec.Size
	}
	keeperSize := resource.Quantity{}
	if instance.Spec.ClickHouse.Keeper.Instance != nil {
		keeperSize = instance.Spec.ClickHouse.Keeper.Instance.DataVolumeClaimSpec.Size
	}
	for i := range pvcs.Items {
		pvc := &pvcs.Items[i]
		desired := keeperSize
		if pvc.Labels[naming.LabelClickHouseComponent] == naming.ClickHouseComponentClickHouse {
			desired = desiredByGroup[pvc.Labels[naming.LabelClickHouseComputeGroup]]
		}
		current := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		if desired.IsZero() || desired.Cmp(current) <= 0 {
			continue
		}
		before := pvc.DeepCopy()
		if pvc.Spec.Resources.Requests == nil {
			pvc.Spec.Resources.Requests = corev1.ResourceList{}
		}
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = desired
		if err = rc.Client().Patch(rc.Context(), pvc, client.MergeFrom(before)); err != nil {
			return err
		}
	}
	return nil
}

func preserveStatefulSetClaimTemplates(rc *context.InstanceContext, desired *appsv1.StatefulSet) error {
	existing := &appsv1.StatefulSet{}
	err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(desired), existing)
	if client.IgnoreNotFound(err) != nil {
		return err
	}
	if err == nil {
		desired.Spec.VolumeClaimTemplates = existing.Spec.VolumeClaimTemplates
	}
	return nil
}

func deleteObsoleteClickHouseResources(rc *context.InstanceContext) error {
	instance := rc.GetInstance()
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		return err
	}
	desiredStatefulSets := map[string]struct{}{}
	for _, plan := range plans {
		desiredStatefulSets[plan.StatefulSet] = struct{}{}
	}
	keeperMembers, err := desiredKeeperMembers(instance)
	if err != nil {
		return err
	}
	for _, member := range keeperMembers {
		desiredStatefulSets[naming.ClickHouseKeeperStatefulSetName(instance.Name, member.Index)] = struct{}{}
	}
	statefulSets := &appsv1.StatefulSetList{}
	selector, err := naming.AsSelector(metav1.LabelSelector{MatchLabels: map[string]string{
		naming.LabelInstance: instance.Name,
		naming.LabelClickHouseEngine: naming.ClickHouseEngine,
	}})
	if err != nil {
		return err
	}
	if err = rc.List(statefulSets, selector); err != nil {
		return err
	}
	deletedKeeper := false
	keeperConfigToDelete := ""
	for i := range statefulSets.Items {
		if _, keep := desiredStatefulSets[statefulSets.Items[i].Name]; keep {
			continue
		}
		if statefulSets.Items[i].Labels[naming.LabelClickHouseComponent] == naming.ClickHouseComponentKeeper {
			if deletedKeeper {
				continue
			}
			deletedKeeper = true
			keeperConfigToDelete = naming.ClickHouseKeeperConfigMapName(instance.Name, parseInt32OrZero(statefulSets.Items[i].Labels[naming.LabelClickHouseReplica]))
		}
		if err = client.IgnoreNotFound(rc.DeleteControlled(&statefulSets.Items[i])); err != nil {
			return err
		}
	}

	desiredServices := map[string]struct{}{}
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		desiredServices[naming.ClickHouseGroupHeadlessServiceName(instance.Name, group.Name)] = struct{}{}
		desiredServices[naming.ClickHouseGroupClientServiceName(instance.Name, group.Name)] = struct{}{}
	}
	services := &corev1.ServiceList{}
	serviceSelector, err := naming.AsSelector(metav1.LabelSelector{MatchLabels: naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentClickHouse)})
	if err != nil {
		return err
	}
	if err = rc.List(services, serviceSelector); err != nil {
		return err
	}
	for i := range services.Items {
		if _, keep := desiredServices[services.Items[i].Name]; keep {
			continue
		}
		if err = client.IgnoreNotFound(rc.DeleteControlled(&services.Items[i])); err != nil {
			return err
		}
	}
	desiredGroupConfigs := map[string]struct{}{}
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		desiredGroupConfigs[naming.ClickHouseGroupConfigMapName(instance.Name, group.Name)] = struct{}{}
	}
	groupConfigs := &corev1.ConfigMapList{}
	if err = rc.List(groupConfigs, serviceSelector); err != nil {
		return err
	}
	for i := range groupConfigs.Items {
		if _, keep := desiredGroupConfigs[groupConfigs.Items[i].Name]; keep {
			continue
		}
		if err = client.IgnoreNotFound(rc.DeleteControlled(&groupConfigs.Items[i])); err != nil {
			return err
		}
	}

	desiredKeeperConfigs := map[string]struct{}{}
	for _, member := range keeperMembers {
		desiredKeeperConfigs[naming.ClickHouseKeeperConfigMapName(instance.Name, member.Index)] = struct{}{}
	}
	keeperConfigs := &corev1.ConfigMapList{}
	keeperSelector, err := naming.AsSelector(metav1.LabelSelector{MatchLabels: naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentKeeper)})
	if err != nil {
		return err
	}
	if err = rc.List(keeperConfigs, keeperSelector); err != nil {
		return err
	}
	for i := range keeperConfigs.Items {
		if _, keep := desiredKeeperConfigs[keeperConfigs.Items[i].Name]; keep {
			continue
		}
		if keeperConfigToDelete != "" && keeperConfigs.Items[i].Name != keeperConfigToDelete {
			continue
		}
		if err = client.IgnoreNotFound(rc.DeleteControlled(&keeperConfigs.Items[i])); err != nil {
			return err
		}
		break
	}
	return nil
}

func parseInt32OrZero(value string) int32 {
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0
	}
	return int32(parsed)
}

func reconcileNextClickHousePodRollout(rc *context.InstanceContext) (bool, error) {
	instance := rc.GetInstance()
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		return false, err
	}
	pods := &corev1.PodList{}
	selector, err := naming.AsSelector(metav1.LabelSelector{MatchLabels: naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentClickHouse)})
	if err != nil {
		return false, err
	}
	if err = rc.List(pods, selector); err != nil {
		return false, err
	}
	backupRevision, err := clickHouseBackupConfigRevision(rc)
	if err != nil {
		return false, err
	}
	credentialRevision, err := clickHouseCredentialRevision(rc)
	if err != nil {
		return false, err
	}
	byOwner := map[string]*corev1.Pod{}
	for i := range pods.Items {
		owner := metav1.GetControllerOf(&pods.Items[i])
		if owner != nil && owner.Kind == "StatefulSet" {
			byOwner[owner.Name] = &pods.Items[i]
		}
	}
	hasOutdated := false
	hasUnhealthyCurrent := false
	for _, plan := range plans {
		pod := byOwner[plan.StatefulSet]
		if pod == nil || desiredHostReplicas(instance, plan.Group, plan.Replica) == 0 {
			continue
		}
		current := pod.Annotations[annotationPodRevision] == podRevision(instance, plan) &&
			pod.Annotations[annotationBackupConfigRevision] == backupRevision &&
			pod.Annotations[annotationCredentialRevision] == credentialRevision
		if !current {
			hasOutdated = true
		} else if pod.Labels[naming.LabelClickHouseRoutable] != "true" {
			hasUnhealthyCurrent = true
		}
	}
	if hasOutdated && hasUnhealthyCurrent {
		setProgressingCondition(instance, metav1.ConditionTrue, "CanaryNotReady", "rollout waits for updated replicas to become serviceable")
		return true, nil
	}
	for _, plan := range rolloutOrder(plans) {
		pod := byOwner[plan.StatefulSet]
		desiredPodRevision := podRevision(instance, plan)
		targetRevision := revisionHash(desiredPodRevision, backupRevision, credentialRevision)
		if pod == nil || (pod.Annotations[annotationPodRevision] == desiredPodRevision && pod.Annotations[annotationBackupConfigRevision] == backupRevision && pod.Annotations[annotationCredentialRevision] == credentialRevision) {
			continue
		}
		oldVersion := pod.Annotations[annotationEngineVersion]
		if oldVersion != "" && !canStartVersionUpgrade(instance, oldVersion) {
			setProgressingCondition(instance, metav1.ConditionFalse, "BackupRequired", "version upgrade requires clickhouse.kdb.com/backup-ready=true")
			return false, fmt.Errorf("clickhouse version upgrade from %s to %s requires backup readiness", oldVersion, instance.Spec.EngineVersion)
		}
		readyInShard := int32(0)
		for i := range pods.Items {
			candidate := &pods.Items[i]
			if candidate.Labels[naming.LabelClickHouseComputeGroup] == plan.Group.Name &&
				candidate.Labels[naming.LabelClickHouseDataShard] == fmt.Sprintf("%d", plan.Shard) &&
				candidate.Labels[naming.LabelClickHouseRoutable] == "true" {
				readyInShard++
			}
		}
		drained := pod.Annotations[annotationRolloutDraining] == targetRevision
		if !drained && !canUpdateReplica(readyInShard, replicasPerShard(plan.Group)) {
			setProgressingCondition(instance, metav1.ConditionTrue, "WaitingForReplica", "waiting for another serviceable replica before rollout")
			return true, nil
		}
		if pod.Labels[naming.LabelClickHouseRoutable] == "true" && !drained {
			before := pod.DeepCopy()
			if pod.Labels == nil {
				pod.Labels = map[string]string{}
			}
			if pod.Annotations == nil {
				pod.Annotations = map[string]string{}
			}
			pod.Labels[naming.LabelClickHouseRoutable] = "false"
			pod.Annotations[annotationRolloutDraining] = targetRevision
			if err = rc.Client().Patch(rc.Context(), pod, client.MergeFrom(before)); err != nil {
				return false, err
			}
			setProgressingCondition(instance, metav1.ConditionTrue, "DrainingReplica", "replica removed from routing before replacement")
			return true, nil
		}
		if err = client.IgnoreNotFound(rc.Client().Delete(rc.Context(), pod)); err != nil {
			return false, err
		}
		setProgressingCondition(instance, metav1.ConditionTrue, "ReplacingReplica", "rolling update replaces one replica per shard")
		return true, nil
	}
	setProgressingCondition(instance, metav1.ConditionFalse, "RolloutComplete", "ClickHouse Pod revisions are current")
	return false, nil
}

func clickHouseRolloutRequeue() time.Duration {
	return 5 * time.Second
}
