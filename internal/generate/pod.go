package generate

import (
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/security"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var tmpDirSizeLimit = resource.MustParse("16Mi")

func instanceVolsIntent(rc *context.InstanceContext, sts *appsv1.StatefulSet) (mounts []corev1.VolumeMount, vols []corev1.Volume) {
	instance := rc.GetInstance()
	instanceSet := naming.InstanceSetSpec(instance)

	// data vol and mount message
	dataVolumeMount := naming.DataVolumeMount()
	mounts = append(mounts, dataVolumeMount)
	vols = append(vols, corev1.Volume{
		Name: dataVolumeMount.Name,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: naming.InstanceDataVolume(sts).Name,
				ReadOnly:  false,
			},
		},
	})

	// log vol and mount message
	if instanceSet.LogVolumeClaimSpec != nil {
		walVolumeMount := naming.LogVolumeMount()
		mounts = append(mounts, walVolumeMount)
		vols = append(vols, corev1.Volume{
			Name: walVolumeMount.Name,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: naming.InstanceLogVolume(sts).Name,
					ReadOnly:  false,
				},
			},
		})

	}

	// downward vol
	downwardAPIVolumeMount := naming.DownwardAPIVolumeMount()
	mounts = append(mounts, downwardAPIVolumeMount)
	vols = append(vols, corev1.Volume{
		Name: downwardAPIVolumeMount.Name,
		VolumeSource: corev1.VolumeSource{
			DownwardAPI: &corev1.DownwardAPIVolumeSource{
				Items: []corev1.DownwardAPIVolumeFile{{
					Path: "cpu_limit",
					ResourceFieldRef: &corev1.ResourceFieldSelector{
						ContainerName: naming.ContainerDatabase,
						Resource:      "limits.cpu",
						Divisor:       naming.OneMillicore,
					},
				}, {
					Path: "cpu_request",
					ResourceFieldRef: &corev1.ResourceFieldSelector{
						ContainerName: naming.ContainerDatabase,
						Resource:      "requests.cpu",
						Divisor:       naming.OneMillicore,
					},
				}, {
					Path: "mem_limit",
					ResourceFieldRef: &corev1.ResourceFieldSelector{
						ContainerName: naming.ContainerDatabase,
						Resource:      "limits.memory",
						Divisor:       naming.OneMebibyte,
					},
				}, {
					Path: "mem_request",
					ResourceFieldRef: &corev1.ResourceFieldSelector{
						ContainerName: naming.ContainerDatabase,
						Resource:      "requests.memory",
						Divisor:       naming.OneMebibyte,
					},
				}, {
					Path: "labels",
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: corev1.SchemeGroupVersion.Version,
						FieldPath:  "metadata.labels",
					},
				}, {
					Path: "annotations",
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: corev1.SchemeGroupVersion.Version,
						FieldPath:  "metadata.annotations",
					},
				}},
			},
		},
	})

	// AddTMPEmptyDir adds a "tmp" EmptyDir volume to the provided Pod template, while then also adding a
	// volume mount at /tmp for all containers defined within the Pod template
	vols = append(vols, corev1.Volume{
		Name: "tmp",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				SizeLimit: &tmpDirSizeLimit,
			},
		},
	})
	mounts = append(mounts,
		corev1.VolumeMount{
			Name:      "tmp",
			MountPath: "/tmp",
		})
	return
}

func instanceContainer(rc *context.InstanceContext, mounts []corev1.VolumeMount) (initContainers []corev1.Container, containers []corev1.Container) {
	instance := rc.GetInstance()
	instanceSet := naming.InstanceSetSpec(instance)
	containers = append(containers, corev1.Container{
		Name:      instanceSet.MainContainer.Name,
		Command:   instanceSet.MainContainer.Command,
		Env:       append(RequestEnvironment(instance), instanceSet.MainContainer.Env...),
		Args:      instanceSet.MainContainer.Args,
		Image:     instanceSet.MainContainer.Image,
		Resources: instanceSet.MainContainer.Resources,

		Ports: []corev1.ContainerPort{{
			Name:          naming.PortDatabase,
			ContainerPort: *instance.Spec.Port,
			Protocol:      corev1.ProtocolTCP,
		}},

		SecurityContext: security.InitRestrictedSecurityContext(),
		VolumeMounts:    mounts,
	})
	containers = append(containers, corev1.Container{
		Name:         instanceSet.SidecarContainer.Name,
		Command:      instanceSet.SidecarContainer.Command,
		Env:          append(RequestEnvironment(instance), instanceSet.SidecarContainer.Env...),
		Args:         instanceSet.SidecarContainer.Args,
		Image:        instanceSet.SidecarContainer.Image,
		Resources:    instanceSet.SidecarContainer.Resources,
		VolumeMounts: mounts,
	})
	return
}

func InstancePodIntent(rc *context.InstanceContext, sts *appsv1.StatefulSet) {
	podTmpl := sts.Spec.Template
	mounts, vols := instanceVolsIntent(rc, sts)
	podTmpl.Spec.Volumes = vols
	initContainer, containers := instanceContainer(rc, mounts)
	for _, c := range containers {
		decorateWithDefaultProbes(&c)
	}
	podTmpl.Spec.InitContainers = initContainer
	podTmpl.Spec.Containers = containers
}

// decorateWithDefaultProbes adds default liveness and readiness probes to container.
func decorateWithDefaultProbes(container *corev1.Container) {

	//
	// If the process does not stop, kubelet will send a SIGKILL after the pod's
	// TerminationGracePeriodSeconds.
	// - https://docs.k8s.io/concepts/workloads/pods/pod-lifecycle/
	//
	// TODO: Consider TerminationGracePeriodSeconds' impact here.
	// TODO: Consider if a PreStop hook is necessary.
	if container.LivenessProbe == nil {
		container.LivenessProbe = &corev1.Probe{
			TimeoutSeconds:   6,
			PeriodSeconds:    3,
			SuccessThreshold: 1,
			FailureThreshold: 3,
		}
		container.LivenessProbe.InitialDelaySeconds = 30
		container.LivenessProbe.Exec = &corev1.ExecAction{
			Command: []string{
				"/kdb/bin/liveness.sh",
			},
		}
	}
	if container.ReadinessProbe == nil {
		container.ReadinessProbe = &corev1.Probe{
			TimeoutSeconds:   6,
			PeriodSeconds:    3,
			SuccessThreshold: 1,
			FailureThreshold: 3,
		}
		container.ReadinessProbe.InitialDelaySeconds = 60
		container.ReadinessProbe.Exec = &corev1.ExecAction{
			Command: []string{
				"/kdb/bin/readiness.sh",
			},
		}
	}

}
