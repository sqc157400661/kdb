package steps

import (
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/util"
)

func TestStoppedInstanceStatefulSetNamesKeepsDesiredSets(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Name = "demo"
	instance.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: util.Int32(2)}

	names := stoppedInstanceStatefulSetNames(instance)

	if !names.Has("demo0") || !names.Has("demo1") {
		t.Fatalf("expected stopped desired StatefulSets to be kept, got %v", names.List())
	}
	if names.Has("demo2") {
		t.Fatalf("expected extra StatefulSet demo2 not to be kept")
	}
}
