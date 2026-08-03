package mysql

import (
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
)

func TestMySQLAllowlistCIDRsAreCanonicalAndDeterministic(t *testing.T) {
	raw, err := json.Marshal([]string{"10.0.0.7", "10.0.0.0/24", "10.0.0.7/32", "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}
	got, configured, err := mysqlAllowlistCIDRs(map[string]string{MySQLAllowlistCIDRsConfigKey: string(raw)})
	if err != nil {
		t.Fatalf("mysqlAllowlistCIDRs() error = %v", err)
	}
	if !configured {
		t.Fatal("configured = false, want true")
	}
	want := []string{"10.0.0.0/24", "10.0.0.7/32", "2001:db8::1/128"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cidrs = %#v, want %#v", got, want)
	}
}

func TestMySQLAllowlistNetworkPolicyKeepsControlPlaneAndEnforcesCIDRs(t *testing.T) {
	port := int32(3307)
	instance := &v1.KDBInstance{}
	instance.Name = "orders"
	instance.Namespace = "database"
	instance.Spec.Port = &port
	policy := mysqlAllowlistNetworkPolicy(instance, []string{"10.20.0.0/16", "192.0.2.8/32"})
	if policy.Name != "orders-mysql-allowlist" || policy.Namespace != "database" {
		t.Fatalf("unexpected policy identity: %s/%s", policy.Namespace, policy.Name)
	}
	if len(policy.Spec.Ingress) != 6 {
		t.Fatalf("ingress rules = %d, want 6", len(policy.Spec.Ingress))
	}
	for index, wantCIDR := range []string{"10.20.0.0/16", "192.0.2.8/32"} {
		rule := policy.Spec.Ingress[4+index]
		if len(rule.From) != 1 || rule.From[0].IPBlock == nil || rule.From[0].IPBlock.CIDR != wantCIDR {
			t.Fatalf("cidr rule %d = %#v, want %s", index, rule, wantCIDR)
		}
		if len(rule.Ports) != 1 || rule.Ports[0].Port == nil || rule.Ports[0].Port.IntValue() != 3307 {
			t.Fatalf("cidr rule port = %#v, want 3307", rule.Ports)
		}
	}
}

func TestMySQLAllowlistAbsentDoesNotChangeExistingNetworking(t *testing.T) {
	got, configured, err := mysqlAllowlistCIDRs(map[string]string{"other": "value"})
	if err != nil {
		t.Fatal(err)
	}
	if configured || got != nil {
		t.Fatalf("got %#v configured=%v, want nil false", got, configured)
	}
}

func TestMySQLAllowlistRejectsInvalidCIDR(t *testing.T) {
	_, _, err := mysqlAllowlistCIDRs(map[string]string{MySQLAllowlistCIDRsConfigKey: `["not-an-address"]`})
	if err == nil {
		t.Fatal("expected invalid CIDR error")
	}
}
