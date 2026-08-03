package clickhouse

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/pkg/errors"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/security"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	clickHouseKeeperConfigVolumeName = "keeper-config"
	clickHouseKeeperDataVolumeName   = "keeper-data"
	clickHouseKeeperConfigMountPath  = "/etc/clickhouse-keeper"
	clickHouseKeeperDataMountPath    = "/var/lib/clickhouse-keeper"
)

func buildKeeperHeadlessService(instance *v1.KDBInstance) (*corev1.Service, error) {
	replicas, err := desiredKeeperReplicas(instance)
	if err != nil {
		return nil, err
	}
	if replicas == 0 {
		return nil, nil
	}
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Namespace: instance.Namespace,
		Name:      naming.ClickHouseKeeperHeadlessServiceName(instance.Name),
		Labels:    naming.Merge(instance.Labels, naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentKeeper)),
	}}
	service.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
	service.Spec = corev1.ServiceSpec{
		ClusterIP:                corev1.ClusterIPNone,
		PublishNotReadyAddresses: true,
		Selector:                 naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentKeeper),
		Ports: []corev1.ServicePort{
			{Name: "client", Port: naming.ClickHouseKeeperClientPort(), TargetPort: intstr.FromString("client"), Protocol: corev1.ProtocolTCP},
			{Name: "raft", Port: naming.ClickHouseKeeperRaftPort(), TargetPort: intstr.FromString("raft"), Protocol: corev1.ProtocolTCP},
		},
	}
	return service, nil
}

func buildKeeperStatefulSets(instance *v1.KDBInstance, serviceAccountName string) ([]*appsv1.StatefulSet, error) {
	members, err := desiredKeeperMembers(instance)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, nil
	}
	if instance.Spec.ClickHouse.Keeper.Instance == nil {
		return nil, fmt.Errorf("clickhouse keeper dedicated mode requires keeper.instance")
	}
	instanceSet := *instance.Spec.ClickHouse.Keeper.Instance
	statefulSets := make([]*appsv1.StatefulSet, 0, len(members))
	for _, member := range members {
		statefulSets = append(statefulSets, buildKeeperStatefulSet(instance, instanceSet, member, serviceAccountName))
	}
	return statefulSets, nil
}

func buildKeeperStatefulSet(instance *v1.KDBInstance, instanceSet shared.InstanceSetSpec, member keeperMember, serviceAccountName string) *appsv1.StatefulSet {
	labels := naming.ClickHouseKeeperLabels(instance.Name, member.Index)
	replicas := util.Int32(1)
	if instance.Spec.Shutdown != nil && *instance.Spec.Shutdown {
		replicas = util.Int32(0)
	}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: instance.Namespace,
		Name:      naming.ClickHouseKeeperStatefulSetName(instance.Name, member.Index),
	}}
	sts.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("StatefulSet"))
	sts.Labels = naming.Merge(instance.Labels, instanceSet.Metadata.GetLabelsOrNil(), labels)
	sts.Annotations = naming.Merge(instance.Annotations, instanceSet.Metadata.GetAnnotationsOrNil())
	sts.Spec.Replicas = replicas
	sts.Spec.ServiceName = naming.ClickHouseKeeperHeadlessServiceName(instance.Name)
	sts.Spec.RevisionHistoryLimit = util.Int32(0)
	sts.Spec.UpdateStrategy.Type = appsv1.OnDeleteStatefulSetStrategyType
	sts.Spec.PersistentVolumeClaimRetentionPolicy = &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
		WhenDeleted: appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
		WhenScaled:  appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
	}
	sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
	sts.Spec.Template.Labels = naming.Merge(instance.Labels, instanceSet.Metadata.GetLabelsOrNil(), labels)
	sts.Spec.Template.Annotations = naming.Merge(instance.Annotations, instanceSet.Metadata.GetAnnotationsOrNil())
	if sts.Spec.Template.Annotations == nil {
		sts.Spec.Template.Annotations = map[string]string{}
	}
	sts.Spec.Template.Annotations[annotationPodRevision] = keeperPodRevision(instance, instanceSet, member)
	sts.Spec.Template.Annotations[annotationEngineVersion] = instance.Spec.EngineVersion
	sts.Spec.Template.Spec.ServiceAccountName = serviceAccountName
	sts.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways
	sts.Spec.Template.Spec.EnableServiceLinks = util.Bool(false)
	sts.Spec.Template.Spec.Affinity = keeperAffinity(instance, instanceSet.Affinity)
	sts.Spec.Template.Spec.Tolerations = instanceSet.Tolerations
	sts.Spec.Template.Spec.TopologySpreadConstraints = instanceSet.TopologySpreadConstraints
	if instanceSet.PriorityClassName != nil {
		sts.Spec.Template.Spec.PriorityClassName = *instanceSet.PriorityClassName
	}
	sts.Spec.Template.Spec.SecurityContext = security.PodSecurityContext(instance)
	sts.Spec.Template.Spec.Volumes = []corev1.Volume{{
		Name: clickHouseKeeperConfigVolumeName,
		VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: naming.ClickHouseKeeperConfigMapName(instance.Name, member.Index)},
			Items: []corev1.KeyToPath{{
				Key:  clickHouseKeeperConfigKey,
				Path: clickHouseKeeperConfigKey,
			}},
		}},
	}}
	sts.Spec.Template.Spec.Containers = []corev1.Container{keeperContainer(instanceSet)}
	sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{keeperDataVolumeClaim(instance, instanceSet, member)}
	return sts
}

func keeperAffinity(instance *v1.KDBInstance, configured *corev1.Affinity) *corev1.Affinity {
	affinity := &corev1.Affinity{}
	if configured != nil {
		affinity = configured.DeepCopy()
	}
	desired, _ := desiredKeeperReplicas(instance)
	if desired <= 1 {
		return affinity
	}
	if affinity.PodAntiAffinity == nil {
		affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
	}
	affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
		affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
		corev1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{MatchLabels: naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentKeeper)},
			TopologyKey:   "kubernetes.io/hostname",
		},
	)
	return affinity
}

func keeperPodRevision(instance *v1.KDBInstance, instanceSet shared.InstanceSetSpec, member keeperMember) string {
	rendered, _ := json.Marshal(instanceSet)
	return revisionHash(instance.Spec.EngineVersion, fmt.Sprintf("%d", member.ID), string(rendered), renderKeeperConfig(mustKeeperMembers(instance), member))
}

func mustKeeperMembers(instance *v1.KDBInstance) []keeperMember {
	members, _ := desiredKeeperMembers(instance)
	return members
}

func keeperContainer(instanceSet shared.InstanceSetSpec) corev1.Container {
	container := corev1.Container{
		Name:            "keeper",
		Command:         instanceSet.MainContainer.Command,
		Args:            instanceSet.MainContainer.Args,
		Image:           instanceSet.MainContainer.Image,
		Env:             instanceSet.MainContainer.Env,
		Resources:       instanceSet.MainContainer.Resources,
		SecurityContext: security.InitClickHouseSecurityContext(),
		Ports: []corev1.ContainerPort{
			{Name: "client", ContainerPort: naming.ClickHouseKeeperClientPort(), Protocol: corev1.ProtocolTCP},
			{Name: "raft", ContainerPort: naming.ClickHouseKeeperRaftPort(), Protocol: corev1.ProtocolTCP},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: clickHouseKeeperConfigVolumeName, MountPath: clickHouseKeeperConfigMountPath, ReadOnly: true},
			{Name: clickHouseKeeperDataVolumeName, MountPath: clickHouseKeeperDataMountPath},
		},
		StartupProbe:   keeperProbe(30, 5),
		ReadinessProbe: keeperProbe(3, 5),
		LivenessProbe:  keeperProbe(6, 10),
	}
	if len(container.Args) == 0 {
		container.Args = []string{"--config-file=" + clickHouseKeeperConfigMountPath + "/" + clickHouseKeeperConfigKey}
	}
	return container
}

func keeperProbe(failureThreshold int32, periodSeconds int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{
			Port: intstr.FromString("client"),
		}},
		FailureThreshold: failureThreshold,
		PeriodSeconds:    periodSeconds,
		TimeoutSeconds:   3,
	}
}

func keeperDataVolumeClaim(instance *v1.KDBInstance, instanceSet shared.InstanceSetSpec, member keeperMember) corev1.PersistentVolumeClaim {
	pvcSpec := instanceSet.DataVolumeClaimSpec
	pvc := corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name:        clickHouseKeeperDataVolumeName,
		Annotations: naming.Merge(instanceSet.Metadata.GetAnnotationsOrNil(), pvcSpec.Metadata.GetAnnotationsOrNil()),
		Labels: naming.Merge(
			instance.Labels,
			instanceSet.Metadata.GetLabelsOrNil(),
			pvcSpec.Metadata.GetLabelsOrNil(),
			naming.ClickHouseKeeperLabels(instance.Name, member.Index)),
	}}
	pvc.Spec = corev1.PersistentVolumeClaimSpec{
		StorageClassName: clickHouseStorageClassName(pvcSpec.StorageClass),
		AccessModes: []corev1.PersistentVolumeAccessMode{
			corev1.ReadWriteOnce,
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: pvcSpec.Size,
			},
		},
	}
	return pvc
}

func reconcileKeeperStatefulSets(rc *context.InstanceContext) error {
	statefulSets, err := buildKeeperStatefulSets(rc.GetInstance(), rc.GetClusterServiceAccountName())
	if err != nil {
		return err
	}
	existing := &appsv1.StatefulSetList{}
	selector, err := naming.AsSelector(metav1.LabelSelector{MatchLabels: naming.ClickHouseLabels(rc.GetInstance().Name, naming.ClickHouseComponentKeeper)})
	if err != nil {
		return err
	}
	if err = rc.List(existing, selector); err != nil {
		return err
	}
	existingNames := map[string]struct{}{}
	desiredByName := make(map[string]*appsv1.StatefulSet, len(statefulSets))
	for _, sts := range statefulSets {
		desiredByName[sts.Name] = sts
	}
	for i := range existing.Items {
		current := &existing.Items[i]
		existingNames[current.Name] = struct{}{}
	}

	// A multi-member Keeper cannot elect a leader until a quorum of its Raft
	// peers exists. Create every missing member during bootstrap before applying
	// the normal one-member-at-a-time readiness and rollout gates. Otherwise the
	// first member waits forever for peers that the reconciler has not created.
	createdMissingMember := false
	for _, sts := range missingKeeperStatefulSets(statefulSets, existingNames) {
		if err = errors.WithStack(rc.SetControllerReference(sts)); err != nil {
			return err
		}
		if err = errors.WithStack(rc.Apply(sts)); err != nil {
			return err
		}
		createdMissingMember = true
	}
	if createdMissingMember {
		return nil
	}

	for i := range existing.Items {
		current := &existing.Items[i]
		desired := desiredByName[current.Name]
		if desired != nil && keeperReplicaReconcileRequired(current, desired) {
			if err = preserveStatefulSetClaimTemplates(rc, desired); err != nil {
				return err
			}
			if err = errors.WithStack(rc.SetControllerReference(desired)); err != nil {
				return err
			}
			return errors.WithStack(rc.Apply(desired))
		}
		if existing.Items[i].Status.ReadyReplicas < 1 {
			return nil
		}
	}
	for _, sts := range statefulSets {
		if err = preserveStatefulSetClaimTemplates(rc, sts); err != nil {
			return err
		}
		if err = errors.WithStack(rc.SetControllerReference(sts)); err != nil {
			return err
		}
		if err = errors.WithStack(rc.Apply(sts)); err != nil {
			return err
		}
	}
	return nil
}

func missingKeeperStatefulSets(desired []*appsv1.StatefulSet, existingNames map[string]struct{}) []*appsv1.StatefulSet {
	missing := make([]*appsv1.StatefulSet, 0, len(desired))
	for _, sts := range desired {
		if _, exists := existingNames[sts.Name]; !exists {
			missing = append(missing, sts)
		}
	}
	return missing
}

func keeperReplicaReconcileRequired(current, desired *appsv1.StatefulSet) bool {
	if current == nil || desired == nil || current.Spec.Replicas == nil || desired.Spec.Replicas == nil {
		return false
	}
	return *current.Spec.Replicas != *desired.Spec.Replicas
}

func keeperReadiness(rc *context.InstanceContext) (int32, int32, error) {
	desired, err := desiredKeeperReplicas(rc.GetInstance())
	if err != nil || desired == 0 {
		return desired, desired, err
	}
	statefulSets := &appsv1.StatefulSetList{}
	selector, err := naming.AsSelector(metav1.LabelSelector{MatchLabels: naming.ClickHouseLabels(rc.GetInstance().Name, naming.ClickHouseComponentKeeper)})
	if err != nil {
		return desired, 0, err
	}
	if err = rc.List(statefulSets, selector); err != nil {
		return desired, 0, err
	}
	readyMembers := int32(0)
	for _, sts := range statefulSets.Items {
		if sts.Status.ReadyReplicas >= 1 {
			readyMembers++
		}
	}
	return desired, readyMembers, nil
}

func gateClickHouseOnKeeper(rc *context.InstanceContext) (reconcile.Result, bool, error) {
	desired, ready, err := keeperReadiness(rc)
	if err != nil {
		setKeeperReadyCondition(rc.GetInstance(), metav1.ConditionFalse, "KeeperReadinessUnknown", err.Error(), desired, ready)
		return reconcile.Result{}, false, err
	}
	if desired == 0 {
		return reconcile.Result{}, true, nil
	}
	if ready < desired {
		setKeeperReadyCondition(rc.GetInstance(), metav1.ConditionFalse, "KeeperQuorumUnavailable", "ClickHouse host creation waits for Keeper readiness", desired, ready)
		return reconcile.Result{RequeueAfter: 10 * time.Second}, false, nil
	}
	setKeeperReadyCondition(rc.GetInstance(), metav1.ConditionTrue, "KeeperQuorumReady", "all dedicated Keeper members are ready", desired, ready)
	return reconcile.Result{}, true, nil
}

func reconcileNextKeeperRollout(rc *context.InstanceContext) (bool, error) {
	instance := rc.GetInstance()
	if instance.Spec.ClickHouse.Keeper.Mode != v1.ClickHouseKeeperDedicated || instance.Spec.ClickHouse.Keeper.Instance == nil {
		return false, nil
	}
	desired, ready, err := keeperReadiness(rc)
	if err != nil || ready < desired {
		return false, err
	}
	members, err := desiredKeeperMembers(instance)
	if err != nil {
		return false, err
	}
	pods := &corev1.PodList{}
	selector, err := naming.AsSelector(metav1.LabelSelector{MatchLabels: naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentKeeper)})
	if err != nil {
		return false, err
	}
	if err = rc.List(pods, selector); err != nil {
		return false, err
	}
	byOwner := map[string]*corev1.Pod{}
	for i := range pods.Items {
		owner := metav1.GetControllerOf(&pods.Items[i])
		if owner != nil && owner.Kind == "StatefulSet" {
			byOwner[owner.Name] = &pods.Items[i]
		}
	}
	for _, member := range members {
		pod := byOwner[naming.ClickHouseKeeperStatefulSetName(instance.Name, member.Index)]
		if pod == nil || pod.Annotations[annotationPodRevision] == keeperPodRevision(instance, *instance.Spec.ClickHouse.Keeper.Instance, member) {
			continue
		}
		oldVersion := pod.Annotations[annotationEngineVersion]
		if oldVersion != "" && !canStartVersionUpgrade(instance, oldVersion) {
			return false, fmt.Errorf("clickhouse Keeper version upgrade from %s to %s requires backup readiness", oldVersion, instance.Spec.EngineVersion)
		}
		if err = client.IgnoreNotFound(rc.Client().Delete(rc.Context(), pod)); err != nil {
			return false, err
		}
		setProgressingCondition(instance, metav1.ConditionTrue, "ReplacingKeeperMember", "rolling update replaces one Keeper member")
		return true, nil
	}
	return false, nil
}

func setKeeperReadyCondition(instance *v1.KDBInstance, status metav1.ConditionStatus, reason, message string, members, readyMembers int32) {
	if instance.Status.ClickHouse == nil {
		instance.Status.ClickHouse = &v1.ClickHouseStatus{DataShards: instance.Spec.ClickHouse.DataShards}
	}
	instance.Status.ClickHouse.Keeper = &v1.ClickHouseKeeperStatus{
		Members:      members,
		ReadyMembers: readyMembers,
	}
	if status != metav1.ConditionTrue && instance.Status.Phase == clickHousePhaseRunning {
		instance.Status.Phase = clickHousePhaseDegraded
	}
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               "KeeperReady",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: instance.Generation,
	})
}

type keeperMemberAction struct {
	Member keeperMember
	Action string
}

func planNextKeeperMemberChange(desired []keeperMember, ready map[int32]bool) []keeperMemberAction {
	for _, member := range desired {
		if !ready[member.Index] {
			return []keeperMemberAction{{Member: member, Action: "CreateOrRepair"}}
		}
	}
	return nil
}
