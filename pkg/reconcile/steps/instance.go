package steps

import (
	"github.com/pkg/errors"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// reconcileInstance writes instance according to spec of cluster.
func reconcileMySQLInstance(rc *context.InstanceContext, runner *appsv1.StatefulSet) (err error) {
	existing := runner.DeepCopy()
	//instance := rc.GetInstance()
	//
	*runner = appsv1.StatefulSet{}
	runner.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("StatefulSet"))
	runner.Namespace, runner.Name = existing.Namespace, existing.Name
	err = rc.SetControllerReference(runner)
	if err != nil {
		return
	}
	generateInstanceStatefulSetIntent(rc, runner)
	// pvc
	var (
	//instanceConfigMap *corev1.ConfigMap
	//instanceCertificates *corev1.Secret
	//postgresDataVolume   *corev1.PersistentVolumeClaim
	//postgresWALVolume    *corev1.PersistentVolumeClaim
	)
	//instanceConfigMap, err = setInstanceConfigMap(rc, runner, setSpec)
	//if err != nil {
	//	return
	//}
	//postgresDataVolume, err = volumes.ReconcilePostgresDataVolume(rc, runner, setSpec)
	//if err != nil {
	//	return
	//}
	//postgresWALVolume, err = volumes.ReconcilePostgresWALVolume(rc, runner, setSpec, instance)
	//if err != nil {
	//	return
	//}
	//initInstancePod(
	//	rc, cluster, setSpec, postgresDataVolume, postgresWALVolume,
	//	&runner.Spec.Template, instanceCertificates, instanceConfigMap)
	// add nss_wrapper init container and add nss_wrapper env vars to the database and pgbackrest containers
	//postgres.AddNSSWrapper(
	//	pg_cluster.PostgresContainerImage(cluster),
	//	cluster.Spec.ImagePullPolicy,
	//	&runner.Spec.Template)
	// add an emptyDir volume to the PodTemplateSpec and an associated '/tmp' volume mount to
	// all containers included within that spec
	//postgres.AddTMPEmptyDir(&runner.Spec.Template)

	// mount shared memory to the Postgres instance
	//postgres.AddDevSHM(&runner.Spec.Template)
	err = errors.WithStack(rc.Apply(runner))
	return
}

func generateInstanceStatefulSetIntent(rc *context.InstanceContext, sts *appsv1.StatefulSet) {
	instance := rc.GetInstance()
	instanceSet := instance.Spec.InstanceSet
	//numInstancePods := rc.GetInstances().AllPodsNum()
	sts.Annotations = naming.Merge(
		instance.Annotations,
		instanceSet.Metadata.GetAnnotationsOrNil())
	sts.Labels = naming.Merge(
		instance.Labels,
		instanceSet.Metadata.GetLabelsOrNil(),
		map[string]string{
			naming.LabelInstanceSet: sts.Name,
			naming.LabelInstance:    instance.Name,
		})
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
		map[string]string{
			naming.LabelInstanceSet: sts.Name,
			naming.LabelInstance:    instance.Name,
		})

	// Don't clutter the namespace with extra ControllerRevisions.
	// The "controller-revision-hash" label still exists on the Pod.
	sts.Spec.RevisionHistoryLimit = util.Int32(0)

	// Give the Pod a stable DNS record based on its name.
	// - https://docs.k8s.io/concepts/workloads/controllers/statefulset/#stable-network-id
	// - https://docs.k8s.io/concepts/services-networking/dns-pod-service/#pods
	//sts.Spec.ServiceName = rc.GetClusterPodService().Name

	// Disable StatefulSet's "RollingUpdate" strategy. The rolloutInstances
	// method considers Pods across the entire PostgresCluster and deletes
	// them to trigger updates.
	// - https://docs.k8s.io/concepts/workloads/controllers/statefulset/#on-delete
	sts.Spec.UpdateStrategy.Type = appsv1.OnDeleteStatefulSetStrategyType

	// Use scheduling constraints from the cluster spec.
	sts.Spec.Template.Spec.Affinity = instanceSet.Affinity
	sts.Spec.Template.Spec.Tolerations = instanceSet.Tolerations
	sts.Spec.Template.Spec.TopologySpreadConstraints = instanceSet.TopologySpreadConstraints

	// this is the designated instance, but
	// - others are still running during shutdown, or
	// - it is time to startup.
	sts.Spec.Replicas = util.Int32(1)

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

	//sts.Spec.Template.Spec.SecurityContext = postgres.PodSecurityContext(cluster)

}

//func setInstanceConfigMap(rc *db_context.Context, sts *appsv1.StatefulSet, setSpec *v1beta1.PostgresInstanceSetSpec) (*corev1.ConfigMap, error) {
//	cluster := rc.PostgresCluster()
//	instanceConfigMap := &corev1.ConfigMap{ObjectMeta: naming.InstanceConfigMap(sts)}
//	instanceConfigMap.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
//
//	err := errors.WithStack(rc.SetControllerReference(instanceConfigMap))
//	if err != nil {
//		return nil, err
//	}
//
//	naming.MergeMetaWithCR(&instanceConfigMap.ObjectMeta, cluster, setSpec)
//	instanceConfigMap.Labels = naming.Merge(
//		instanceConfigMap.Labels,
//		map[string]string{
//			naming.LabelInstance: sts.Name,
//		})
//
//	conf := config.GetPatroniConfigForInstance(cluster, setSpec)
//	if !status.ClusterBootstrapped(cluster) {
//		conf = helper.UnsafeMergeMap(conf, config.GetPatroniBootstrapConfigForInstance(cluster, setSpec))
//	}
//	confByte, err := yaml.Marshal(conf)
//	if err != nil {
//		return nil, err
//	}
//	helper.StringMap(&instanceConfigMap.Data)
//	instanceConfigMap.Data[config.PatroniConfigMapFileKey] = string(append([]byte(config.YamlGeneratedWarning), confByte...))
//	instanceConfigMap.Data[config.QueriesConfigFileKey] = config.QueriesConfig
//	err = errors.WithStack(rc.CSAApply(instanceConfigMap))
//	if err != nil {
//		return nil, err
//	}
//	return instanceConfigMap, err
//}
