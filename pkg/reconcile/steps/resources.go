package steps

import corev1 "k8s.io/api/core/v1"

func isEmptyResourceRequirements(resources corev1.ResourceRequirements) bool {
	return len(resources.Limits) == 0 &&
		len(resources.Requests) == 0
}
