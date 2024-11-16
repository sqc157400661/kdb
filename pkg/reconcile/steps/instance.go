package steps

import (
	"github.com/pkg/errors"
	"github.com/sqc157400661/kdb/internal/generate"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
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
		naming.LabelCluster:     naming.KDBInstanceCluster(instance),
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
		naming.LabelCluster:     naming.KDBInstanceCluster(instance),
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
	return
}
