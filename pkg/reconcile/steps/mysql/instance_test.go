package mysql

import (
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/topology"
)

func int32ptr(v int32) *int32 { return &v }

func TestResolveMGRSeeds(t *testing.T) {
	ins := &v1.KDBInstance{}
	ins.Name = "demo"
	ins.Namespace = "ns"
	ins.Spec.DeployArch = naming.MySQLMGRDeployArch
	ins.Spec.Leader = v1.HostInfo{Port: 3306}
	ins.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: int32ptr(3)}

	seeds := topology.BuildMGRSeeds(ins, topology.DefaultMGRGroupPort)
	want := "demo0-0.demo.ns.svc.cluster.local:33061,demo1-0.demo.ns.svc.cluster.local:33061,demo2-0.demo.ns.svc.cluster.local:33061"
	if seeds != want {
		t.Fatalf("unexpected seeds: %s", seeds)
	}
}
