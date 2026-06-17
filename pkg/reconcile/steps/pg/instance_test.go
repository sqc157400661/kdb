package pg

import (
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/util"
)

func TestBuildPatroniConfigDefaults(t *testing.T) {
	port := int32(5432)
	instance := &v1.KDBInstance{}
	instance.Name = "demo-pg"
	instance.Namespace = "kdb"
	instance.Spec.Engine = naming.PostgresEngine
	instance.Spec.EngineVersion = "14"
	instance.Spec.Port = &port
	instance.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: util.Int32(1)}

	conf, err := buildPatroniConfig(instance)
	if err != nil {
		t.Fatalf("buildPatroniConfig returned error: %v", err)
	}

	if got := conf["scope"]; got != "demo-pg" {
		t.Fatalf("unexpected scope: %v", got)
	}
	postgresql := conf["postgresql"].(map[string]interface{})
	if got := postgresql["data_dir"]; got != "/pgdata/pg14" {
		t.Fatalf("unexpected data_dir: %v", got)
	}
	if _, ok := postgresql["connect_address"]; ok {
		t.Fatalf("postgresql connect_address should be provided by PATRONI_POSTGRESQL_CONNECT_ADDRESS env")
	}
	restapi := conf["restapi"].(map[string]interface{})
	if _, ok := restapi["connect_address"]; ok {
		t.Fatalf("restapi connect_address should be provided by PATRONI_RESTAPI_CONNECT_ADDRESS env")
	}
	kubernetes := conf["kubernetes"].(map[string]interface{})
	if got := kubernetes["use_endpoints"]; got != true {
		t.Fatalf("expected kubernetes use_endpoints=true, got %v", got)
	}
}

func TestBuildPatroniConfigDefaultsEngineVersion(t *testing.T) {
	port := int32(5432)
	instance := &v1.KDBInstance{}
	instance.Name = "demo-pg"
	instance.Namespace = "kdb"
	instance.Spec.Engine = naming.PostgresEngine
	instance.Spec.Port = &port
	instance.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: util.Int32(1)}

	conf, err := buildPatroniConfig(instance)
	if err != nil {
		t.Fatalf("buildPatroniConfig returned error: %v", err)
	}

	postgresql := conf["postgresql"].(map[string]interface{})
	if got := postgresql["data_dir"]; got != "/pgdata/pg14" {
		t.Fatalf("unexpected default data_dir: %v", got)
	}
	if got := postgresql["bin_dir"]; got != "/usr/lib/postgresql/14/bin" {
		t.Fatalf("unexpected default bin_dir: %v", got)
	}
}

func TestBuildPatroniConfigRejectsEtcdUntilImplemented(t *testing.T) {
	port := int32(5432)
	instance := &v1.KDBInstance{}
	instance.Name = "demo-pg"
	instance.Namespace = "kdb"
	instance.Spec.Engine = naming.PostgresEngine
	instance.Spec.EngineVersion = "14"
	instance.Spec.Port = &port
	instance.Spec.PostgreSQL = &v1.PostgreSQLSpec{
		Patroni: &v1.PostgreSQLPatroniSpec{DCS: v1.PostgreSQLDCSEtcd},
	}

	if _, err := buildPatroniConfig(instance); err == nil {
		t.Fatalf("expected unsupported etcd DCS error")
	}
}
