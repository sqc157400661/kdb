package topology

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
)

func chInt32(v int32) *int32 { return &v }

func chBool(v bool) *bool { return &v }

func TestValidateClickHouseSpec(t *testing.T) {
	cases := []struct {
		name      string
		instance  *v1.KDBInstance
		old       *v1.KDBInstance
		wantError string
	}{
		{
			name:     "non clickhouse ignored",
			instance: &v1.KDBInstance{Spec: v1.KDBInstanceSpec{Engine: naming.MySQLEngine}},
		},
		{
			name:      "missing spec",
			instance:  &v1.KDBInstance{Spec: v1.KDBInstanceSpec{Engine: naming.ClickHouseEngine}},
			wantError: "spec.clickhouse is required when engine is clickhouse",
		},
		{
			name:      "valid standalone",
			instance:  clickHouseInstance(validClickHouseSpec()),
			wantError: "",
		},
		{
			name: "invalid data shards",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.DataShards = 0
			})),
			wantError: "dataShards",
		},
		{
			name: "duplicate compute group names",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.ComputeGroups = append(spec.ComputeGroups, v1.ClickHouseComputeGroupSpec{
					Name:     "ingest",
					Role:     v1.ClickHouseRoleServing,
					Instance: shared.InstanceSetSpec{Replicas: chInt32(1)},
				})
			})),
			wantError: "must be unique",
		},
		{
			name: "missing ingest",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.ComputeGroups[0].Role = v1.ClickHouseRoleServing
			})),
			wantError: "exactly one Ingest compute group is required",
		},
		{
			name: "two ingest groups",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.ComputeGroups = append(spec.ComputeGroups, v1.ClickHouseComputeGroupSpec{
					Name:     "ingest-b",
					Role:     v1.ClickHouseRoleIngest,
					Instance: shared.InstanceSetSpec{Replicas: chInt32(1)},
				})
			})),
			wantError: "exactly one Ingest compute group is required",
		},
		{
			name: "invalid keeper size",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.Keeper.Replicas = chInt32(2)
			})),
			wantError: "keeper replicas must be 1, 3, or 5",
		},
		{
			name: "shared keeper requires ref",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.Keeper = v1.ClickHouseKeeperSpec{Mode: v1.ClickHouseKeeperSharedRef}
			})),
			wantError: "sharedRef mode requires ref",
		},
		{
			name: "shared keeper rejects dedicated fields",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.Keeper = v1.ClickHouseKeeperSpec{
					Mode:     v1.ClickHouseKeeperSharedRef,
					Ref:      &corev1.LocalObjectReference{Name: "keeper"},
					Replicas: chInt32(3),
				}
			})),
			wantError: "must not set replicas or instance",
		},
		{
			name: "ingest auto suspend rejected",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.ComputeGroups[0].Lifecycle = &v1.ClickHouseComputeGroupLifecycleSpec{AutoSuspendEnabled: chBool(true)}
			})),
			wantError: "ingest compute group must not enable auto suspend",
		},
		{
			name: "production gateway requires serving",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.Keeper.Replicas = chInt32(3)
				spec.Gateway = &v1.ClickHouseGatewaySpec{Enabled: chBool(true)}
			})),
			wantError: "requires at least one Serving",
		},
		{
			name: "production gateway rejects one keeper",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.Gateway = &v1.ClickHouseGatewaySpec{Enabled: chBool(true)}
				spec.ComputeGroups = append(spec.ComputeGroups, v1.ClickHouseComputeGroupSpec{
					Name:     "serving",
					Role:     v1.ClickHouseRoleServing,
					Instance: shared.InstanceSetSpec{Replicas: chInt32(2)},
				})
			})),
			wantError: "production keeper replicas must not be 1",
		},
		{
			name: "data shards immutable",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.DataShards = 3
			})),
			old:       clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) { spec.DataShards = 2 })),
			wantError: "dataShards is immutable",
		},
		{
			name: "pvc shrink rejected",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.ComputeGroups[0].Instance.DataVolumeClaimSpec.Size = resource.MustParse("10Gi")
			})),
			old: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.ComputeGroups[0].Instance.DataVolumeClaimSpec.Size = resource.MustParse("20Gi")
			})),
			wantError: "pvc size must not shrink",
		},
		{
			name: "replica change requires confirmation",
			instance: clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.ComputeGroups[0].Instance.Replicas = chInt32(2)
			})),
			old:       clickHouseInstance(validClickHouseSpec()),
			wantError: "requires confirmation annotation",
		},
		{
			name: "replica change with confirmation",
			instance: withClickHouseInstanceAnnotations(clickHouseInstance(withClickHouseSpec(func(spec *v1.ClickHouseSpec) {
				spec.ComputeGroups[0].Instance.Replicas = chInt32(2)
			})), map[string]string{
				"clickhouse.kdb.com/replica-change-confirmed": "ingest:1->2",
			}),
			old: clickHouseInstance(validClickHouseSpec()),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateClickHouseSpec(c.instance, c.old)
			if c.wantError == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.wantError != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", c.wantError)
				}
				if !strings.Contains(err.Error(), c.wantError) {
					t.Fatalf("error = %q, want containing %q", err.Error(), c.wantError)
				}
			}
		})
	}
}

func TestValidateClickHouseSpecDoesNotMutate(t *testing.T) {
	instance := clickHouseInstance(validClickHouseSpec())
	beforeReplicas := *instance.Spec.ClickHouse.Keeper.Replicas
	beforeGroupReplicas := *instance.Spec.ClickHouse.ComputeGroups[0].Instance.Replicas

	if err := ValidateClickHouseSpec(instance, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *instance.Spec.ClickHouse.Keeper.Replicas != beforeReplicas {
		t.Fatalf("keeper replicas mutated")
	}
	if *instance.Spec.ClickHouse.ComputeGroups[0].Instance.Replicas != beforeGroupReplicas {
		t.Fatalf("compute group replicas mutated")
	}
}

func withClickHouseInstanceAnnotations(instance *v1.KDBInstance, annotations map[string]string) *v1.KDBInstance {
	instance.Annotations = annotations
	return instance
}

func clickHouseInstance(spec *v1.ClickHouseSpec) *v1.KDBInstance {
	return &v1.KDBInstance{
		Spec: v1.KDBInstanceSpec{
			Engine:     naming.ClickHouseEngine,
			ClickHouse: spec,
		},
	}
}

func validClickHouseSpec() *v1.ClickHouseSpec {
	return withClickHouseSpec(nil)
}

func withClickHouseSpec(modify func(*v1.ClickHouseSpec)) *v1.ClickHouseSpec {
	spec := &v1.ClickHouseSpec{
		DataShards: 1,
		Keeper: v1.ClickHouseKeeperSpec{
			Mode:     v1.ClickHouseKeeperDedicated,
			Replicas: chInt32(1),
		},
		ComputeGroups: []v1.ClickHouseComputeGroupSpec{{
			Name: "ingest",
			Role: v1.ClickHouseRoleIngest,
			Instance: shared.InstanceSetSpec{
				Replicas: chInt32(1),
				DataVolumeClaimSpec: shared.PVCSpec{
					StorageClass: "standard",
					Size:         resource.MustParse("20Gi"),
				},
			},
		}},
	}
	if modify != nil {
		modify(spec)
	}
	return spec
}
