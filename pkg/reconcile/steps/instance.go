package steps

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/sqc157400661/kdb/internal/generate"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/security"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// reconcileInstance writes instance according to spec of cluster.
func reconcileMySQLInstance(rc *context.InstanceContext, runner *appsv1.StatefulSet) (err error) {
	runner.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("StatefulSet"))
	err = rc.SetControllerReference(runner)
	if err != nil {
		return
	}
	generate.InstanceStatefulSetIntent(rc, runner)
	// pvc
	err = reconcileDataVolume(rc, runner)
	if err != nil {
		return
	}
	err = reconcileLogVolume(rc, runner)
	if err != nil {
		return
	}
	generate.InstancePodIntent(rc, runner)
	err = errors.WithStack(rc.Apply(runner))
	return
}

// reconcileDataVolume writes the PersistentVolumeClaim for kdb instance data volume.
func reconcileDataVolume(rc *context.InstanceContext, runner *appsv1.StatefulSet) error {
	instance := rc.GetInstance()
	instanceVolumes := rc.Volumes()
	labelMap := map[string]string{
		naming.LabelClusterID:   naming.KDBInstanceClusterID(instance),
		naming.LabelInstanceSet: runner.Name,
		naming.LabelInstance:    instance.Name,
		naming.LabelData:        naming.Engine(instance),
	}

	var pvc *corev1.PersistentVolumeClaim
	existingPVCName, err := getPVCName(labelMap, instanceVolumes)
	if err != nil {
		return errors.WithStack(err)
	}
	if existingPVCName != "" {
		pvc = &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Namespace: instance.GetNamespace(),
			Name:      existingPVCName,
		}}
	} else {
		pvc = &corev1.PersistentVolumeClaim{ObjectMeta: naming.InstanceDataVolume(runner)}
	}

	pvc.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PersistentVolumeClaim"))

	err = errors.WithStack(rc.SetControllerReference(pvc))
	dataPvcSpec := naming.InstanceDataPvcSpec(instance)
	instanceSet := naming.InstanceSetSpec(instance)
	pvc.Annotations = naming.Merge(
		instanceSet.Metadata.GetAnnotationsOrNil(),
		dataPvcSpec.Metadata.GetAnnotationsOrNil())

	pvc.Labels = naming.Merge(
		instanceSet.Metadata.GetLabelsOrNil(),
		dataPvcSpec.Metadata.GetLabelsOrNil(),
		labelMap,
	)
	pvc.Spec = corev1.PersistentVolumeClaimSpec{
		StorageClassName: &dataPvcSpec.StorageClass,
		AccessModes: []corev1.PersistentVolumeAccessMode{
			corev1.ReadWriteOnce,
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: dataPvcSpec.Size,
			},
		},
	}
	if err == nil {
		err = rc.HandlePersistentVolumeClaimError(errors.WithStack(rc.Apply(pvc)))
	}
	return err
}

// reconcileLogVolume writes the PersistentVolumeClaim for kdb instance log volume.
func reconcileLogVolume(rc *context.InstanceContext, runner *appsv1.StatefulSet) error {
	instance := rc.GetInstance()
	instanceSet := naming.InstanceSetSpec(instance)
	if instanceSet.LogVolumeClaimSpec == nil {
		return nil
	}
	instanceVolumes := rc.Volumes()
	labelMap := map[string]string{
		naming.LabelClusterID:   naming.KDBInstanceClusterID(instance),
		naming.LabelInstanceSet: runner.Name,
		naming.LabelInstance:    instance.Name,
		naming.LabelLog:         naming.Engine(instance),
	}

	var pvc *corev1.PersistentVolumeClaim
	existingPVCName, err := getPVCName(labelMap, instanceVolumes)
	if err != nil {
		return errors.WithStack(err)
	}
	if existingPVCName != "" {
		pvc = &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Namespace: instance.GetNamespace(),
			Name:      existingPVCName,
		}}
	} else {
		pvc = &corev1.PersistentVolumeClaim{ObjectMeta: naming.InstanceLogVolume(runner)}
	}
	pvc.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PersistentVolumeClaim"))
	err = errors.WithStack(rc.SetControllerReference(pvc))
	logPvcSpec := naming.InstanceLogPvcSpec(instance)
	pvc.Annotations = naming.Merge(
		instanceSet.Metadata.GetAnnotationsOrNil(),
		logPvcSpec.Metadata.GetAnnotationsOrNil())

	pvc.Labels = naming.Merge(
		instanceSet.Metadata.GetLabelsOrNil(),
		logPvcSpec.Metadata.GetLabelsOrNil(),
		labelMap,
	)
	pvc.Spec = corev1.PersistentVolumeClaimSpec{
		StorageClassName: &logPvcSpec.StorageClass,
		AccessModes: []corev1.PersistentVolumeAccessMode{
			corev1.ReadWriteOnce,
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: logPvcSpec.Size,
			},
		},
	}
	if err == nil {
		err = rc.HandlePersistentVolumeClaimError(errors.WithStack(rc.Apply(pvc)))
	}

	return err
}

// getPVCName returns the name of a PVC that has the provided labels, if found.
func getPVCName(labelMap map[string]string,
	volumes []corev1.PersistentVolumeClaim,
) (string, error) {

	selector, err := naming.AsSelector(metav1.LabelSelector{
		MatchLabels: labelMap,
	})
	if err != nil {
		return "", errors.WithStack(err)
	}

	for _, pvc := range volumes {
		if selector.Matches(labels.Set(pvc.GetLabels())) {
			return pvc.GetName(), nil
		}
	}

	return "", nil
}

// reconcileInstance writes instance according to spec of cluster.
func reconcilePGInstance(rc *context.InstanceContext, runner *appsv1.StatefulSet) (err error) {
	runner.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("StatefulSet"))
	err = rc.SetControllerReference(runner)
	if err != nil {
		return
	}
	generate.InstanceStatefulSetIntent(rc, runner)
	err = reconcileDataVolume(rc, runner)
	if err != nil {
		return
	}
	err = reconcileLogVolume(rc, runner)
	if err != nil {
		return
	}
	postgresPodIntent(rc, runner)
	err = errors.WithStack(rc.Apply(runner))
	return
}

func postgresPodIntent(rc *context.InstanceContext, sts *appsv1.StatefulSet) {
	instance := rc.GetInstance()
	instanceSet := naming.InstanceSetSpec(instance)
	port := naming.KDBInstanceMasterPort(instance)
	if port == 0 {
		port = 5432
	}
	engineVersion := instance.Spec.EngineVersion
	if engineVersion == "" {
		engineVersion = "14"
	}
	pgData := naming.PostgreSQLDataMountPath + "/pg" + engineVersion
	pgWAL := naming.PostgreSQLWALMountPath + "/pg" + engineVersion + "_wal"

	volumes := []corev1.Volume{
		{
			Name: "postgres-data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: naming.InstanceDataVolume(sts).Name,
					ReadOnly:  false,
				},
			},
		},
		{
			Name: "patroni-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: naming.InstanceConfigMap(instance).Name,
					},
				},
			},
		},
		{
			Name: "postgres-tmp",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "postgres-dshm",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumMemory,
				},
			},
		},
	}

	mounts := []corev1.VolumeMount{
		{Name: "postgres-data", MountPath: naming.PostgreSQLDataMountPath},
		{Name: "patroni-config", MountPath: naming.PatroniConfigMountPath, ReadOnly: true},
		{Name: "postgres-tmp", MountPath: "/tmp"},
		{Name: "postgres-dshm", MountPath: "/dev/shm"},
	}
	if instanceSet.LogVolumeClaimSpec != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "postgres-wal",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: naming.InstanceLogVolume(sts).Name,
					ReadOnly:  false,
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: "postgres-wal", MountPath: naming.PostgreSQLWALMountPath})
	}

	envs := []corev1.EnvVar{
		{Name: "PGDATA", Value: pgData},
		{Name: "PGHOST", Value: naming.PostgreSQLSocketDirectory},
		{Name: "PGPORT", Value: fmt.Sprint(port)},
		{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		{Name: "POD_IP", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"}}},
		{Name: "KDB_INSTANCE_NAME", Value: instance.Name},
		{Name: "KDB_NAMESPACE", Value: instance.Namespace},
		{Name: "KDB_ENGINE", Value: naming.Engine(instance)},
	}

	database := corev1.Container{
		Name:            naming.ContainerDatabase,
		Command:         []string{"patroni", naming.PatroniConfigMountPath},
		Env:             append(envs, instanceSet.MainContainer.Env...),
		Image:           instanceSet.MainContainer.Image,
		Resources:       instanceSet.MainContainer.Resources,
		SecurityContext: security.InitRestrictedSecurityContext(),
		Ports: []corev1.ContainerPort{
			{Name: naming.PortDatabase, ContainerPort: port, Protocol: corev1.ProtocolTCP},
			{Name: "patroni", ContainerPort: 8008, Protocol: corev1.ProtocolTCP},
		},
		VolumeMounts: mounts,
		LivenessProbe: &corev1.Probe{
			InitialDelaySeconds: 30,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			FailureThreshold:    6,
			ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{
				Command: []string{"/kdb/bin/patroni-liveness.sh", "8008"},
			}},
		},
		ReadinessProbe: &corev1.Probe{
			InitialDelaySeconds: 10,
			PeriodSeconds:       5,
			TimeoutSeconds:      5,
			FailureThreshold:    6,
			ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{
				Command: []string{"/kdb/bin/patroni-readiness.sh", "8008"},
			}},
		},
	}
	if len(instanceSet.MainContainer.Command) > 0 {
		database.Command = instanceSet.MainContainer.Command
	}
	if len(instanceSet.MainContainer.Args) > 0 {
		database.Args = instanceSet.MainContainer.Args
	}

	startup := corev1.Container{
		Name: "postgres-startup",
		Command: []string{
			"bash",
			"-ceu",
			fmt.Sprintf("chown -R 999:999 %s %s %s && exec /kdb/bin/postgres-startup.sh %s %s",
				naming.PostgreSQLDataMountPath,
				naming.PostgreSQLWALMountPath,
				naming.PostgreSQLSocketDirectory,
				engineVersion,
				pgWAL),
		},
		Env:             envs,
		Image:           instanceSet.MainContainer.Image,
		Resources:       instanceSet.MainContainer.Resources,
		SecurityContext: security.InitSecurityContextForStartUp(),
		VolumeMounts:    mounts,
	}
	startup.SecurityContext.RunAsUser = util.Int64(0)

	fsGroupPolicy := corev1.FSGroupChangeOnRootMismatch
	sts.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
		FSGroup:             util.Int64(999),
		FSGroupChangePolicy: &fsGroupPolicy,
		SupplementalGroups:  instance.Spec.SupplementalGroups,
	}
	sts.Spec.Template.Spec.Volumes = volumes
	sts.Spec.Template.Spec.InitContainers = []corev1.Container{startup}
	sts.Spec.Template.Spec.Containers = []corev1.Container{database}
}
