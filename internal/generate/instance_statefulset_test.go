package generate

import (
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
)

func TestInstanceStatefulSetReplicasFromShutdown(t *testing.T) {
	tests := []struct {
		name     string
		shutdown *bool
		want     int32
	}{
		{name: "nil shutdown starts pod", shutdown: nil, want: 1},
		{name: "false shutdown starts pod", shutdown: boolPtr(false), want: 1},
		{name: "true shutdown stops pod", shutdown: boolPtr(true), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &v1.KDBInstance{}
			instance.Spec.Shutdown = tt.shutdown

			replicas := instanceStatefulSetReplicas(instance)
			if replicas == nil {
				t.Fatalf("expected replicas to be set")
			}
			if got := *replicas; got != tt.want {
				t.Fatalf("unexpected replicas: got %d, want %d", got, tt.want)
			}
		})
	}
}

func boolPtr(v bool) *bool {
	return &v
}
