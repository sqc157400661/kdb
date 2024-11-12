package security

import (
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/util"
	corev1 "k8s.io/api/core/v1"
)

// InitPodSecurityContext returns a v1.PodSecurityContext with some defaults.
func InitPodSecurityContext() *corev1.PodSecurityContext {
	onRootMismatch := corev1.FSGroupChangeOnRootMismatch
	return &corev1.PodSecurityContext{
		// If set to "OnRootMismatch", if the root of the volume already has
		// the correct permissions, the recursive permission change can be skipped
		FSGroupChangePolicy: &onRootMismatch,
	}
}

// InitRestrictedSecurityContext returns a v1.SecurityContext with safe defaults.
// See https://docs.k8s.io/concepts/security/pod-security-standards/
func InitRestrictedSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		// Prevent any container processes from gaining privileges.
		AllowPrivilegeEscalation: util.Bool(true),

		// Drop any capabilities granted by the container runtime.
		// This must be uppercase to pass Pod Security Admission.
		// - https://releases.k8s.io/v1.24.0/staging/src/k8s.io/pod-security-admission/policy/check_capabilities_restricted.go
		Capabilities: &corev1.Capabilities{
			Add: []corev1.Capability{"LINUX_IMMUTABLE", "NET_ADMIN", "SYS_ADMIN"},
		},

		// Processes in privileged containers are essentially root on the host.
		Privileged: util.Bool(false),

		//RunAsUser: helper.Int64(26),

		// Limit filesystem changes to volumes that are mounted read-write.
		//ReadOnlyRootFilesystem: helper.Bool(true),
		// Fail to start the container if its image runs as UID 0 (root).
		//RunAsNonRoot: helper.Bool(false),
	}
}

func InitSecurityContextForStartUp() *corev1.SecurityContext {
	return &corev1.SecurityContext{

		// Drop any capabilities granted by the container runtime.
		// This must be uppercase to pass Pod Security Admission.
		// - https://releases.k8s.io/v1.24.0/staging/src/k8s.io/pod-security-admission/policy/check_capabilities_restricted.go
		Capabilities: &corev1.Capabilities{
			Add: []corev1.Capability{"LINUX_IMMUTABLE", "NET_ADMIN", "SYS_ADMIN"},
		},
		// Processes in privileged containers are essentially root on the host.
		Privileged: util.Bool(true),
	}
}

// PodSecurityContext returns a v1.PodSecurityContext for instance that can write
// to PersistentVolumes.
func PodSecurityContext(instance *v1.KDBInstance) *corev1.PodSecurityContext {
	podSecurityContext := InitPodSecurityContext()

	// Use the specified supplementary groups except for root. The CRD has
	// similar validation, but we should never emit a PodSpec with that group.
	// - https://docs.k8s.io/concepts/security/pod-security-standards/
	for i := range instance.Spec.SupplementalGroups {
		if gid := instance.Spec.SupplementalGroups[i]; gid > 0 {
			podSecurityContext.SupplementalGroups =
				append(podSecurityContext.SupplementalGroups, gid)
		}
	}

	podSecurityContext.FSGroup = util.Int64(26)

	return podSecurityContext
}
