package generate

import (
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
)

func TestRequestEnvironmentIncludesRoleFromPodLabel(t *testing.T) {
	port := int32(3306)
	instance := &v1.KDBInstance{}
	instance.Name = "demo"
	instance.Namespace = "default"
	instance.Labels = map[string]string{"kdb.clusterID": "c1"}
	instance.Spec.Port = &port
	instance.Spec.InstanceSet = shared.InstanceSetSpec{}

	envs := RequestEnvironment(instance, "demo0")

	for _, env := range envs {
		if env.Name != "ROLE" {
			continue
		}
		if env.ValueFrom == nil || env.ValueFrom.FieldRef == nil {
			t.Fatalf("ROLE env should come from fieldRef")
		}
		if got, want := env.ValueFrom.FieldRef.FieldPath, "metadata.labels['kdb.role']"; got != want {
			t.Fatalf("unexpected ROLE fieldPath: got %q, want %q", got, want)
		}
		return
	}

	t.Fatalf("ROLE env not found")
}

func TestRequestEnvironmentIncludesMGREnvs(t *testing.T) {
	port := int32(3306)
	replicas := int32(3)
	instance := &v1.KDBInstance{}
	instance.Name = "demo"
	instance.Namespace = "default"
	instance.Spec.Port = &port
	instance.Spec.DeployArch = naming.MySQLMGRDeployArch
	instance.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: &replicas}

	envs := RequestEnvironment(instance, "demo1")
	values := map[string]string{}
	for _, env := range envs {
		values[env.Name] = env.Value
	}

	if values["ENABLE_MGR"] != "1" {
		t.Fatalf("ENABLE_MGR not set")
	}
	if values["MGR_LOCAL_ADDRESS"] != "demo1-0.demo.default.svc.cluster.local:33061" {
		t.Fatalf("unexpected local address: %s", values["MGR_LOCAL_ADDRESS"])
	}
	if values["MGR_BOOTSTRAP"] != "false" {
		t.Fatalf("unexpected bootstrap flag: %s", values["MGR_BOOTSTRAP"])
	}
}
