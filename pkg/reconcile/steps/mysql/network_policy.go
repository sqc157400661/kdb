package mysql

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
)

const MySQLAllowlistCIDRsConfigKey = "mysql.allowlistCIDRs"

func reconcileMySQLAllowlistNetworkPolicy(rc *context.InstanceContext, instance *v1.KDBInstance) error {
	cidrs, configured, err := mysqlAllowlistCIDRs(instance.Spec.Config)
	if err != nil {
		return err
	}
	if !configured {
		return nil
	}
	policy := mysqlAllowlistNetworkPolicy(instance, cidrs)
	if err := errors.WithStack(rc.SetControllerReference(policy)); err != nil {
		return fmt.Errorf("set mysql allowlist NetworkPolicy controller reference: %w", err)
	}
	if err := errors.WithStack(rc.Apply(policy)); err != nil {
		return fmt.Errorf("apply mysql allowlist NetworkPolicy: %w", err)
	}
	return nil
}

func mysqlAllowlistCIDRs(config map[string]string) ([]string, bool, error) {
	raw, configured := config[MySQLAllowlistCIDRsConfigKey]
	if !configured {
		return nil, false, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", MySQLAllowlistCIDRsConfigKey, err)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		cidr, err := canonicalMySQLAllowlistCIDR(value)
		if err != nil {
			return nil, true, err
		}
		if _, ok := seen[cidr]; ok {
			continue
		}
		seen[cidr] = struct{}{}
		result = append(result, cidr)
	}
	sort.Strings(result)
	return result, true, nil
}

func canonicalMySQLAllowlistCIDR(value string) (string, error) {
	value = strings.TrimSpace(value)
	if ip := net.ParseIP(value); ip != nil {
		if ip.To4() != nil {
			return ip.String() + "/32", nil
		}
		return ip.String() + "/128", nil
	}
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		return "", fmt.Errorf("invalid mysql allowlist CIDR %q: %w", value, err)
	}
	return network.String(), nil
}

func mysqlAllowlistNetworkPolicy(instance *v1.KDBInstance, cidrs []string) *networkingv1.NetworkPolicy {
	tcp := corev1.ProtocolTCP
	databasePort := int32(3306)
	if instance.Spec.Port != nil && *instance.Spec.Port > 0 {
		databasePort = *instance.Spec.Port
	}
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name + "-mysql-allowlist",
			Namespace: instance.Namespace,
			Labels:    map[string]string{naming.LabelInstance: instance.Name},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{naming.LabelInstance: instance.Name}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				// Replication, MGR and Pod-local sidecars must continue to communicate.
				{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{naming.LabelInstance: instance.Name}}}}},
				{
					From:  []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kdb.io/mysql-client": "allowed"}}}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: mysqlPolicyPort(int(databasePort))}},
				},
				{
					From: []networkingv1.NetworkPolicyPeer{
						{PodSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app.kubernetes.io/name", Operator: metav1.LabelSelectorOpIn, Values: []string{"kdb-operator", "kdb-cluster-gateway"}}}}},
						{PodSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: metav1.LabelSelectorOpIn, Values: []string{"kdb-operator", "kdb-cluster-gateway"}}}}},
					},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: mysqlPolicyPort(int(databasePort))}, {Protocol: &tcp, Port: mysqlPolicyPort(8080)}},
				},
				{
					From:  []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kdb-observability"}}}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: mysqlPolicyPort(8080)}, {Protocol: &tcp, Port: mysqlPolicyPort(9104)}},
				},
			},
		},
	}
	for _, cidr := range cidrs {
		policy.Spec.Ingress = append(policy.Spec.Ingress, networkingv1.NetworkPolicyIngressRule{
			From:  []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: cidr}}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: mysqlPolicyPort(int(databasePort))}},
		})
	}
	policy.SetGroupVersionKind(networkingv1.SchemeGroupVersion.WithKind("NetworkPolicy"))
	return policy
}

func mysqlPolicyPort(value int) *intstr.IntOrString {
	port := intstr.FromInt(value)
	return &port
}
