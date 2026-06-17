package rbac

import (
	"os"
	"path/filepath"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

func TestKDBInstancePodPermissionsAreGrantableByOperatorManifests(t *testing.T) {
	operatorRules := loadClusterRoleRules(t, filepath.Join("..", "..", "config", "rbac", "role.yaml"))
	sampleRules := loadRoleRules(t, filepath.Join("..", "..", "hack", "sample", "operator", "role.yaml"))

	for _, rule := range KDBInstancePodPermissions() {
		for _, apiGroup := range rule.APIGroups {
			for _, resource := range rule.Resources {
				for _, verb := range rule.Verbs {
					assertRuleAllows(t, "config/rbac/role.yaml", operatorRules, apiGroup, resource, verb)
					assertRuleAllows(t, "hack/sample/operator/role.yaml", sampleRules, apiGroup, resource, verb)
				}
			}
		}
	}
}

func TestOperatorManifestsCanManageInstanceRbacObjects(t *testing.T) {
	operatorRules := loadClusterRoleRules(t, filepath.Join("..", "..", "config", "rbac", "role.yaml"))
	sampleRules := loadRoleRules(t, filepath.Join("..", "..", "hack", "sample", "operator", "role.yaml"))

	requiredRules := []struct {
		apiGroup string
		resource string
		verbs    []string
	}{
		{
			apiGroup: "",
			resource: "serviceaccounts",
			verbs:    []string{"create", "get", "list", "patch", "update", "watch"},
		},
		{
			apiGroup: "rbac.authorization.k8s.io",
			resource: "roles",
			verbs:    []string{"create", "get", "list", "patch", "update", "watch"},
		},
		{
			apiGroup: "rbac.authorization.k8s.io",
			resource: "rolebindings",
			verbs:    []string{"create", "get", "list", "patch", "update", "watch"},
		},
	}

	for _, rule := range requiredRules {
		for _, verb := range rule.verbs {
			assertRuleAllows(t, "config/rbac/role.yaml", operatorRules, rule.apiGroup, rule.resource, verb)
			assertRuleAllows(t, "hack/sample/operator/role.yaml", sampleRules, rule.apiGroup, rule.resource, verb)
		}
	}
}

func loadClusterRoleRules(t *testing.T, path string) []rbacv1.PolicyRule {
	t.Helper()

	data := readManifest(t, path)
	var role rbacv1.ClusterRole
	if err := yaml.Unmarshal(data, &role); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return role.Rules
}

func loadRoleRules(t *testing.T, path string) []rbacv1.PolicyRule {
	t.Helper()

	data := readManifest(t, path)
	var role rbacv1.Role
	if err := yaml.Unmarshal(data, &role); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return role.Rules
}

func readManifest(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func assertRuleAllows(t *testing.T, manifest string, rules []rbacv1.PolicyRule, apiGroup, resource, verb string) {
	t.Helper()

	for _, rule := range rules {
		if contains(rule.APIGroups, apiGroup) && contains(rule.Resources, resource) && contains(rule.Verbs, verb) {
			return
		}
	}

	t.Fatalf("%s does not grant %q on %q in API group %q", manifest, verb, resource, apiGroup)
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
