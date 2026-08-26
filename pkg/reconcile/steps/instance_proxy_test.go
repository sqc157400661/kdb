package steps

import (
	"testing"

	"github.com/sqc157400661/kdb/internal/naming"
)

func TestDesiredInstanceProxySQLSecretDataBindsMonitorToMySQLCredential(t *testing.T) {
	data, err := desiredInstanceProxySQLSecretData(
		map[string][]byte{"admin-username": []byte("admin"), "admin-password": []byte("preserved"), "custom": []byte("keep")},
		map[string][]byte{naming.MySQLMonitorPasswordSecretKey: []byte("mysql-monitor")},
	)
	if err != nil {
		t.Fatalf("desired secret: %v", err)
	}
	if string(data["admin-password"]) != "preserved" || string(data["custom"]) != "keep" {
		t.Fatalf("existing data was not preserved: %#v", data)
	}
	if string(data["monitor-username"]) != "_monitor_user" || string(data["monitor-password"]) != "mysql-monitor" {
		t.Fatalf("monitor binding = username %q password %q", data["monitor-username"], data["monitor-password"])
	}
}

func TestDesiredInstanceProxySQLSecretDataRejectsMissingMySQLCredential(t *testing.T) {
	if _, err := desiredInstanceProxySQLSecretData(nil, nil); err == nil {
		t.Fatal("missing MySQL monitor credential was accepted")
	}
}
