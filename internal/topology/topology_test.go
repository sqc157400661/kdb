package topology

import (
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
)

func int32ptr(v int32) *int32 { return &v }

func TestValidateInstanceSpec(t *testing.T) {
	cases := []struct {
		name      string
		arch      string
		replicas  int32
		shouldErr bool
	}{
		{"ms-valid", naming.MySQLMasterSlaveDeployArch, 2, false},
		{"ms-invalid", naming.MySQLMasterSlaveDeployArch, 3, true},
		{"mr-valid", naming.MySQLMasterReplicaDeployArch, 3, false},
		{"mr-invalid", naming.MySQLMasterReplicaDeployArch, 1, true},
		{"mgr-valid", naming.MySQLMGRDeployArch, 3, false},
		{"mgr-invalid", naming.MySQLMGRDeployArch, 2, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ins := &v1.KDBInstance{Spec: v1.KDBInstanceSpec{DeployArch: c.arch, InstanceSet: shared.InstanceSetSpec{Replicas: int32ptr(c.replicas)}}}
			err := ValidateInstanceSpec(ins)
			if c.shouldErr && err == nil {
				t.Fatalf("expected error")
			}
			if !c.shouldErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestResolveInstancePlan(t *testing.T) {
	ins := &v1.KDBInstance{
		ObjectMeta: v1.KDBInstance{}.ObjectMeta,
		Spec: v1.KDBInstanceSpec{
			DeployArch:  naming.MySQLMasterReplicaDeployArch,
			InstanceSet: shared.InstanceSetSpec{Replicas: int32ptr(3)},
		},
	}
	plan, err := ResolveInstancePlan(ins)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Roles[0] != RolePrimary || plan.Roles[1] != RoleReplica || plan.Roles[2] != RoleReplica {
		t.Fatalf("unexpected roles: %+v", plan.Roles)
	}
}

func TestResolveMGRConfigDefaults(t *testing.T) {
	ins := &v1.KDBInstance{}
	ins.Name = "demo"
	ins.Namespace = "ns"
	ins.Spec.DeployArch = naming.MySQLMGRDeployArch
	ins.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: int32ptr(3)}

	cfg, err := ResolveMGRConfig(ins)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("expected mgr enabled")
	}
	if cfg.Mode != MGRModeSinglePrimary {
		t.Fatalf("unexpected mode: %s", cfg.Mode)
	}
	if cfg.GroupPort != DefaultMGRGroupPort {
		t.Fatalf("unexpected group port: %d", cfg.GroupPort)
	}
	if cfg.Seeds != "demo0-0.demo.ns.svc.cluster.local:33061,demo1-0.demo.ns.svc.cluster.local:33061,demo2-0.demo.ns.svc.cluster.local:33061" {
		t.Fatalf("unexpected seeds: %s", cfg.Seeds)
	}
}

func TestResolveMGRConfigTypedSpecOverridesLegacy(t *testing.T) {
	bootstrapOrdinal := int32(1)
	groupPort := int32(33062)
	ins := &v1.KDBInstance{}
	ins.Name = "demo"
	ins.Namespace = "ns"
	ins.Spec.DeployArch = naming.MySQLMGRDeployArch
	ins.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: int32ptr(3)}
	ins.Spec.Config = map[string]string{
		"mysql.mgr.mode":             string(MGRModeSinglePrimary),
		"mysql.mgr.bootstrapOrdinal": "0",
		"mysql.mgr.groupPort":        "33061",
	}
	ins.Spec.MySQL = &v1.MySQLSpec{MGR: &v1.MySQLMGRSpec{
		Mode:             string(MGRModeMultiPrimary),
		BootstrapOrdinal: &bootstrapOrdinal,
		GroupPort:        &groupPort,
	}}

	cfg, err := ResolveMGRConfig(ins)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Mode != MGRModeMultiPrimary {
		t.Fatalf("unexpected mode: %s", cfg.Mode)
	}
	if cfg.BootstrapOrdinal != bootstrapOrdinal {
		t.Fatalf("unexpected bootstrap ordinal: %d", cfg.BootstrapOrdinal)
	}
	if cfg.GroupPort != groupPort {
		t.Fatalf("unexpected group port: %d", cfg.GroupPort)
	}
}

func TestResolveMGRConfigInvalidMode(t *testing.T) {
	ins := &v1.KDBInstance{}
	ins.Name = "demo"
	ins.Namespace = "ns"
	ins.Spec.DeployArch = naming.MySQLMGRDeployArch
	ins.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: int32ptr(3)}
	ins.Spec.MySQL = &v1.MySQLSpec{MGR: &v1.MySQLMGRSpec{Mode: "invalid"}}

	if _, err := ResolveMGRConfig(ins); err == nil {
		t.Fatalf("expected invalid mode error")
	}
}
