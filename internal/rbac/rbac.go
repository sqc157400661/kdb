package rbac

import (
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

// KDBInstancePodPermissions returns the RBAC rules needs for KDB instance.
func KDBInstancePodPermissions() []rbacv1.PolicyRule {
	rules := make([]rbacv1.PolicyRule, 0, 3)

	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{v1.SchemeGroupVersion.Group},
		Resources: []string{"kdbinstances"},
		Verbs:     []string{"get", "list", "patch", "watch"},
	})

	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{corev1.SchemeGroupVersion.Group},
		Resources: []string{"pods"},
		Verbs:     []string{"get", "list", "patch", "watch"},
	})

	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{corev1.SchemeGroupVersion.Group},
		Resources: []string{"configmaps", "endpoints"},
		Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
	})

	return rules
}

// PostgreSQLInstancePodPermissions preserves the legacy engine permissions but
// restricts Endpoints to migration reads and adds the Lease verbs required by
// kdb-ha. MySQL and ClickHouse continue using KDBInstancePodPermissions.
func PostgreSQLInstancePodPermissions() []rbacv1.PolicyRule {
	legacy := KDBInstancePodPermissions()
	rules := append([]rbacv1.PolicyRule{}, legacy[:2]...)
	rules = append(rules,
		rbacv1.PolicyRule{
			APIGroups: []string{corev1.SchemeGroupVersion.Group}, Resources: []string{"configmaps"},
			Verbs: []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		},
		rbacv1.PolicyRule{
			APIGroups: []string{corev1.SchemeGroupVersion.Group}, Resources: []string{"endpoints"},
			Verbs: []string{"get", "list", "watch"},
		},
	)
	return append(rules, rbacv1.PolicyRule{
		APIGroups: []string{"coordination.k8s.io"},
		Resources: []string{"leases"},
		Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
	})
}
