package rbac

import (
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

// KDBInstancePodPermissions returns the RBAC rules needs for KDB instance.
func KDBInstancePodPermissions() []rbacv1.PolicyRule {
	rules := make([]rbacv1.PolicyRule, 0, 4)

	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{v1.SchemeGroupVersion.Group},
		Resources: []string{"MySQLInstances"},
		Verbs:     []string{"get", "list", "patch", "watch"},
	})

	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{corev1.SchemeGroupVersion.Group},
		Resources: []string{"pods"},
		Verbs:     []string{"get", "list", "patch", "watch"},
	})

	return rules
}
