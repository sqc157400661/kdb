package clickhouse

import (
	"fmt"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
)

type clickHouseHostPlan struct {
	Group       v1.ClickHouseComputeGroupSpec
	GroupIndex  int32
	Shard       int32
	Replica     int32
	StatefulSet string
	Labels      map[string]string
}

func buildClickHouseHostPlans(instance *v1.KDBInstance) ([]clickHouseHostPlan, error) {
	if instance == nil || instance.Spec.ClickHouse == nil {
		return nil, fmt.Errorf("spec.clickhouse is required when engine is clickhouse")
	}
	spec := instance.Spec.ClickHouse
	if spec.DataShards < 1 {
		return nil, fmt.Errorf("clickhouse dataShards must be greater than or equal to 1")
	}
	plans := make([]clickHouseHostPlan, 0)
	for groupIndex, group := range spec.ComputeGroups {
		replicas := int32(1)
		if group.Instance.Replicas != nil {
			replicas = *group.Instance.Replicas
		}
		for shard := int32(0); shard < spec.DataShards; shard++ {
			for replica := int32(0); replica < replicas; replica++ {
				plans = append(plans, clickHouseHostPlan{
					Group:       group,
					GroupIndex:  int32(groupIndex),
					Shard:       shard,
					Replica:     replica,
					StatefulSet: naming.ClickHouseStatefulSetName(instance.Name, group.Name, shard, replica),
					Labels:      naming.ClickHouseHostLabels(instance.Name, group.Name, shard, replica),
				})
			}
		}
	}
	if len(plans) == 0 {
		return nil, fmt.Errorf("clickhouse computeGroups must not be empty")
	}
	return plans, nil
}

func plansForGroup(plans []clickHouseHostPlan, group string) []clickHouseHostPlan {
	groupPlans := make([]clickHouseHostPlan, 0)
	for _, plan := range plans {
		if plan.Group.Name == group {
			groupPlans = append(groupPlans, plan)
		}
	}
	return groupPlans
}

func replicasPerShard(group v1.ClickHouseComputeGroupSpec) int32 {
	if group.Instance.Replicas == nil {
		return 1
	}
	return *group.Instance.Replicas
}
