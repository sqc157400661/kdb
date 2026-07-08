package clickhouse

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	gatewayBindingJSONKey = "bindings.json"
	gatewayBindingYAMLKey = "bindings.yaml"
	gatewayConfigKey      = "chproxy.yml"
)

type gatewayBindingSpec struct {
	Bindings []gatewayBinding `json:"bindings"`
}

type gatewayBinding struct {
	Account      string `json:"account"`
	ComputeGroup string `json:"computeGroup"`
}

func parseGatewayBindingSecret(secret *corev1.Secret, instance *v1.KDBInstance) (gatewayBindingSpec, error) {
	if secret == nil {
		return gatewayBindingSpec{}, fmt.Errorf("gateway binding secret is nil")
	}
	var raw []byte
	switch {
	case len(secret.Data[gatewayBindingJSONKey]) > 0:
		raw = secret.Data[gatewayBindingJSONKey]
	case len(secret.Data[gatewayBindingYAMLKey]) > 0:
		data, err := yaml.ToJSON(secret.Data[gatewayBindingYAMLKey])
		if err != nil {
			return gatewayBindingSpec{}, err
		}
		raw = data
	default:
		return gatewayBindingSpec{}, fmt.Errorf("gateway binding secret must contain %s or %s", gatewayBindingJSONKey, gatewayBindingYAMLKey)
	}
	var spec gatewayBindingSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return gatewayBindingSpec{}, err
	}
	for i := range spec.Bindings {
		spec.Bindings[i].Account = strings.TrimSpace(spec.Bindings[i].Account)
		spec.Bindings[i].ComputeGroup = strings.TrimSpace(spec.Bindings[i].ComputeGroup)
	}
	if err := validateGatewayBindings(spec, instance, secret); err != nil {
		return gatewayBindingSpec{}, err
	}
	return spec, nil
}

func validateGatewayBindings(spec gatewayBindingSpec, instance *v1.KDBInstance, secret *corev1.Secret) error {
	if len(spec.Bindings) == 0 {
		return fmt.Errorf("gateway bindings must not be empty")
	}
	groups := map[string]struct{}{}
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		groups[group.Name] = struct{}{}
	}
	accounts := map[string]struct{}{}
	passwordKeys := map[string]string{}
	for i, binding := range spec.Bindings {
		account := strings.TrimSpace(binding.Account)
		group := strings.TrimSpace(binding.ComputeGroup)
		if account == "" {
			return fmt.Errorf("gateway bindings[%d].account is required", i)
		}
		if group == "" {
			return fmt.Errorf("gateway bindings[%d].computeGroup is required", i)
		}
		if _, ok := groups[group]; !ok {
			return fmt.Errorf("gateway binding references unknown compute group: %s", group)
		}
		if _, exists := accounts[account]; exists {
			return fmt.Errorf("gateway account must route to exactly one compute group: %s", account)
		}
		passwordKey := gatewayPasswordEnvName(account)
		if existingAccount, exists := passwordKeys[passwordKey]; exists && existingAccount != account {
			return fmt.Errorf("gateway accounts %s and %s map to the same password key %s", existingAccount, account, passwordKey)
		}
		if len(secret.Data[passwordKey]) == 0 {
			return fmt.Errorf("gateway binding secret is missing password key %s for account %s", passwordKey, account)
		}
		passwordKeys[passwordKey] = account
		accounts[account] = struct{}{}
	}
	return nil
}

func renderGatewayConfig(instance *v1.KDBInstance, spec gatewayBindingSpec) string {
	var b strings.Builder
	b.WriteString("server:\n")
	b.WriteString(fmt.Sprintf("  http:\n    listen_addr: \":%d\"\n", naming.ClickHouseGatewayHTTPPort()))
	b.WriteString("users:\n")
	for _, binding := range spec.Bindings {
		b.WriteString(fmt.Sprintf("  - name: %q\n", binding.Account))
		b.WriteString(fmt.Sprintf("    to_cluster: %q\n", "kdb_"+binding.ComputeGroup+"_group"))
		b.WriteString(fmt.Sprintf("    to_user: %q\n", binding.Account))
		b.WriteString("    password: \"${")
		b.WriteString(gatewayPasswordEnvName(binding.Account))
		b.WriteString("}\"\n")
	}
	b.WriteString("clusters:\n")
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		b.WriteString(fmt.Sprintf("  - name: %q\n", "kdb_"+group.Name+"_group"))
		b.WriteString("    scheme: \"http\"\n")
		b.WriteString("    nodes:\n")
		b.WriteString(fmt.Sprintf("      - %q\n", naming.ClickHouseGroupClientServiceName(instance.Name, group.Name)+":8123"))
		b.WriteString("    users:\n")
		for _, binding := range spec.Bindings {
			if binding.ComputeGroup != group.Name {
				continue
			}
			b.WriteString(fmt.Sprintf("      - name: %q\n", binding.Account))
			b.WriteString("        password: \"${")
			b.WriteString(gatewayPasswordEnvName(binding.Account))
			b.WriteString("}\"\n")
		}
	}
	return b.String()
}

func gatewayPasswordEnvName(account string) string {
	var b strings.Builder
	b.WriteString("CHPROXY_PASSWORD_")
	for _, r := range strings.ToUpper(strings.TrimSpace(account)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func gatewayConfigRevision(instance *v1.KDBInstance, bindings gatewayBindingSpec, secret *corev1.Secret) string {
	parts := []string{renderGatewayConfig(instance, bindings)}
	keys := make([]string, 0, len(secret.Data))
	for key := range secret.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key, string(secret.Data[key]))
	}
	return revisionHash(parts...)
}

func mustPlansForGateway(instance *v1.KDBInstance, group string) []clickHouseHostPlan {
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		return nil
	}
	return plansForGroup(plans, group)
}
