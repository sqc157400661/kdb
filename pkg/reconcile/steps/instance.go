package steps

import (
	"github.com/pkg/errors"
	"github.com/sqc157400661/kdb/internal/generate"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	appsv1 "k8s.io/api/apps/v1"
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
	generate.InstanceStatefulSetIntent(rc, runner)
	// pvc
	var (
	//postgresDataVolume   *corev1.PersistentVolumeClaim
	//postgresWALVolume    *corev1.PersistentVolumeClaim
	)
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
