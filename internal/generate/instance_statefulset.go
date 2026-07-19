package generate

import (
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/security"
	"github.com/sqc157400661/kdb/internal/topology"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
)

func InstanceStatefulSetIntent(rc *context.InstanceContext, sts *appsv1.StatefulSet) {
	instance := rc.GetInstance()
	instanceSet := instance.Spec.InstanceSet
	sts.Annotations = naming.Merge(
		instance.Annotations,
		instanceSet.Metadata.GetAnnotationsOrNil())
	labels := map[string]string{
		naming.LabelInstanceSet: sts.Name,
		naming.LabelInstance:    instance.Name,
	}
	if role := instanceSetRole(instance, sts.Name); role != "" {
		labels[naming.LabelRole] = role
	}
	sts.Labels = naming.Merge(
		instance.Labels,
		instanceSet.Metadata.GetLabelsOrNil(),
		labels)
	sts.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: map[string]string{
			naming.LabelInstanceSet: sts.Name,
			naming.LabelInstance:    instance.Name,
		},
	}
	sts.Spec.Template.Annotations = naming.Merge(
		instance.Annotations,
		instanceSet.Metadata.GetAnnotationsOrNil(),
	)
	sts.Spec.Template.Labels = naming.Merge(
		instance.Labels,
		instanceSet.Metadata.GetLabelsOrNil(),
		labels)

	// Don't clutter the namespace with extra ControllerRevisions.
	// The "controller-revision-hash" label still exists on the Pod.
	sts.Spec.RevisionHistoryLimit = util.Int32(0)

	// Give the Pod a stable DNS record based on its name.
	// - https://docs.k8s.io/concepts/workloads/controllers/statefulset/#stable-network-id
	// - https://docs.k8s.io/concepts/services-networking/dns-pod-service/#pods
	if service := rc.GetInstancePodService(); service != nil {
		sts.Spec.ServiceName = service.Name
	} else {
		sts.Spec.ServiceName = naming.InstancePodServiceName(instance.Name)
	}

	// Disable StatefulSet's "RollingUpdate" strategy. The rolloutInstances
	// method considers Pods across the entire Kdb instance and deletes
	// them to trigger updates.
	// - https://docs.k8s.io/concepts/workloads/controllers/statefulset/#on-delete
	sts.Spec.UpdateStrategy.Type = appsv1.OnDeleteStatefulSetStrategyType

	// Use scheduling constraints from the cluster spec.
	sts.Spec.Template.Spec.Affinity = instanceSet.Affinity
	sts.Spec.Template.Spec.Tolerations = instanceSet.Tolerations
	sts.Spec.Template.Spec.TopologySpreadConstraints = instanceSet.TopologySpreadConstraints
	if instanceSet.PriorityClassName != nil {
		sts.Spec.Template.Spec.PriorityClassName = *instanceSet.PriorityClassName
	}

	sts.Spec.Replicas = instanceStatefulSetReplicas(instance)

	// Restart containers any time they stop, die, are killed, etc.
	// - https://docs.k8s.io/concepts/workloads/pods/pod-lifecycle/#restart-policy
	sts.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways

	// ShareProcessNamespace makes Kubernetes' pause process PID 1 and lets
	// containers see each other's processes.
	// - https://docs.k8s.io/tasks/configure-pod-container/share-process-namespace/
	sts.Spec.Template.Spec.ShareProcessNamespace = util.Bool(true)

	// https://patroni.readthedocs.io/en/latest/SETTINGS.html#postgresql callbacks
	sts.Spec.Template.Spec.ServiceAccountName = rc.GetClusterServiceAccountName()

	// Disable environment variables for services other than the Kubernetes API.
	// - https://docs.k8s.io/concepts/services-networking/connect-applications-service/#accessing-the-service
	// - https://releases.k8s.io/v1.23.0/pkg/kubelet/kubelet_pods.go#L553-L563
	sts.Spec.Template.Spec.EnableServiceLinks = util.Bool(false)

	sts.Spec.Template.Spec.SecurityContext = security.PodSecurityContext(instance)

}

func instanceStatefulSetReplicas(instance *v1.KDBInstance) *int32 {
	if instance.Spec.Shutdown != nil && *instance.Spec.Shutdown {
		return util.Int32(0)
	}
	return util.Int32(1)
}

// instanceSetRole maps the topology role of a StatefulSet ordinal into legacy label roles.
//
// It keeps compatibility with existing master/replica labels while deriving decisions from
// the unified topology plan instead of ad-hoc deployArch branches.
func instanceSetRole(instance *v1.KDBInstance, setName string) string {
	if naming.IsPGEngine(instance) {
		// PostgreSQL traffic roles are projected only after the Operator has
		// validated the current Lease term and kdb-ha runtime status.
		return ""
	}
	plan, err := topology.ResolveInstancePlan(instance)
	if err != nil {
		return ""
	}
	idx := naming.InstanceStsNum(instance, setName)
	role, ok := plan.Roles[idx]
	if !ok {
		return ""
	}
	switch role {
	case topology.RolePrimary:
		return naming.MasterRole
	case topology.RoleReplica:
		return naming.ReplicaRole
	case topology.RolePeer:
		if idx == 0 {
			return naming.MasterRole
		}
		return naming.ReplicaRole
	case topology.RoleMGRNode:
		if idx == plan.Primary {
			return naming.MasterRole
		}
		return naming.ReplicaRole
	default:
		return ""
	}
}
