package topology

import (
	"fmt"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
)

type MemberRole string

const (
	RolePrimary MemberRole = "primary"
	RoleReplica MemberRole = "replica"
	RolePeer    MemberRole = "peer"
	RoleMGRNode MemberRole = "mgr"
)

type InstancePlan struct {
	DeployArch string
	Replicas   int
	Primary    int
	Roles      map[int]MemberRole
}

type ClusterPlan struct {
	DeployArch   string
	PrimaryIndex int
	PeerIndex    int
}

// ValidateInstanceSpec enforces deploy architecture constraints for a single KDBInstance.
//
// It validates only architecture-level replica rules (Master-Slave=2, Master-Replica>=2,
// MGR>=3) and is intentionally side-effect free so it can be reused at API/reconcile boundaries.
func ValidateInstanceSpec(instance *v1.KDBInstance) error {
	if instance == nil || instance.Spec.InstanceSet.Replicas == nil {
		return nil
	}
	replicas := int(*instance.Spec.InstanceSet.Replicas)
	return validateByArch(instance.Spec.DeployArch, replicas)
}

// ValidateClusterSpec enforces deploy architecture constraints for a cluster spec.
//
// For KDBCluster, the effective replica cardinality is the number of entries in spec.instances,
// so validation is performed against len(spec.instances).
func ValidateClusterSpec(cluster *v1.KDBCluster) error {
	if cluster == nil {
		return nil
	}
	return validateByArch(cluster.Spec.DeployArch, len(cluster.Spec.Instances))
}

func validateByArch(arch string, replicas int) error {
	switch arch {
	case naming.MySQLMasterSlaveDeployArch:
		if replicas != 2 {
			return fmt.Errorf("when deployArch is %s, replicas must be 2", naming.MySQLMasterSlaveDeployArch)
		}
	case naming.MySQLMasterReplicaDeployArch:
		if replicas < 2 {
			return fmt.Errorf("when deployArch is %s, replicas must be greater than or equal to 2", naming.MySQLMasterReplicaDeployArch)
		}
	case naming.MySQLMGRDeployArch:
		if replicas < 3 {
			return fmt.Errorf("when deployArch is %s, replicas must be greater than or equal to 3", naming.MySQLMGRDeployArch)
		}
	}
	return nil
}

// ResolveInstancePlan builds a normalized per-instance topology plan from deployArch and replicas.
//
// The plan returns a stable role map by ordinal and a primary ordinal used by downstream steps
// (leader defaulting, role labeling, and config rendering).
func ResolveInstancePlan(instance *v1.KDBInstance) (InstancePlan, error) {
	plan := InstancePlan{Roles: map[int]MemberRole{}}
	if instance == nil || instance.Spec.InstanceSet.Replicas == nil {
		return plan, nil
	}
	plan.DeployArch = instance.Spec.DeployArch
	plan.Replicas = int(*instance.Spec.InstanceSet.Replicas)
	if err := validateByArch(plan.DeployArch, plan.Replicas); err != nil {
		return plan, err
	}
	switch plan.DeployArch {
	case naming.MySQLMasterSlaveDeployArch:
		plan.Primary = 0
		for i := 0; i < plan.Replicas; i++ {
			plan.Roles[i] = RolePeer
		}
	case naming.MySQLMasterReplicaDeployArch:
		primary := 0
		if instance.Spec.Leader.PodName != "" {
			primary = naming.InstanceStsNum(instance, trimPodName(instance.Spec.Leader.PodName))
		}
		plan.Primary = primary
		for i := 0; i < plan.Replicas; i++ {
			if i == primary {
				plan.Roles[i] = RolePrimary
			} else {
				plan.Roles[i] = RoleReplica
			}
		}
	case naming.MySQLMGRDeployArch:
		plan.Primary = 0
		for i := 0; i < plan.Replicas; i++ {
			plan.Roles[i] = RoleMGRNode
		}
	default:
		for i := 0; i < plan.Replicas; i++ {
			if i == 0 {
				plan.Roles[i] = RolePrimary
			} else {
				plan.Roles[i] = RoleReplica
			}
		}
	}
	return plan, nil
}

// LeaderForInstance returns the upstream host info a node should follow under the given plan.
//
// For Master-Slave, each side points to its peer (bi-directional relationship).
// For other modes, followers point to the plan primary.
func LeaderForInstance(instance *v1.KDBInstance, plan InstancePlan, selfIndex int) v1.HostInfo {
	if instance == nil {
		return v1.HostInfo{}
	}
	if plan.DeployArch == naming.MySQLMasterSlaveDeployArch {
		peer := 0
		if selfIndex == 0 {
			peer = 1
		}
		return v1.HostInfo{
			PodName: naming.InstancePodName(instance.Name, peer),
			Host:    naming.InstancePodHost(instance.Name, naming.InstancePodServiceName(instance.Name), instance.Namespace, peer),
			Port:    naming.KDBInstanceMasterPort(instance),
		}
	}
	primary := plan.Primary
	return v1.HostInfo{
		PodName: naming.InstancePodName(instance.Name, primary),
		Host:    naming.InstancePodHost(instance.Name, naming.InstancePodServiceName(instance.Name), instance.Namespace, primary),
		Port:    naming.KDBInstanceMasterPort(instance),
	}
}

// ResolveClusterPlan computes cluster-level primary/peer indexes from cluster deployArch.
//
// The result is the single source used by ScaleUp and instance generation, so cluster and
// instance paths do not diverge on leader selection semantics.
func ResolveClusterPlan(cluster *v1.KDBCluster) (ClusterPlan, error) {
	plan := ClusterPlan{PrimaryIndex: 0, PeerIndex: -1}
	if cluster == nil {
		return plan, nil
	}
	plan.DeployArch = cluster.Spec.DeployArch
	if err := ValidateClusterSpec(cluster); err != nil {
		return plan, err
	}
	if len(cluster.Spec.Instances) == 0 {
		return plan, nil
	}
	if !naming.IsEmptyLeader(cluster.Spec.Leader) {
		for idx, ins := range cluster.Spec.Instances {
			if naming.InstancePodName(ins.Name, 0) == cluster.Spec.Leader.PodName {
				plan.PrimaryIndex = idx
				break
			}
		}
	}
	if plan.DeployArch == naming.MySQLMasterSlaveDeployArch {
		plan.PeerIndex = 1
		if plan.PrimaryIndex == 1 {
			plan.PeerIndex = 0
		}
	}
	return plan, nil
}

// ClusterLeaders converts a resolved ClusterPlan into leader host references.
//
// Master-Slave returns two endpoints (primary + peer); other modes return the single primary.
func ClusterLeaders(cluster *v1.KDBCluster, plan ClusterPlan) []*v1.HostInfo {
	if cluster == nil || len(cluster.Spec.Instances) == 0 {
		return nil
	}
	leaders := []*v1.HostInfo{{
		PodName: naming.InstancePodName(cluster.Spec.Instances[plan.PrimaryIndex].Name, 0),
	}}
	if plan.DeployArch == naming.MySQLMasterSlaveDeployArch && plan.PeerIndex >= 0 && plan.PeerIndex < len(cluster.Spec.Instances) {
		leaders = append(leaders, &v1.HostInfo{PodName: naming.InstancePodName(cluster.Spec.Instances[plan.PeerIndex].Name, 0)})
	}
	return leaders
}

func trimPodName(podName string) string {
	if len(podName) > 2 && podName[len(podName)-2:] == "-0" {
		return podName[:len(podName)-2]
	}
	return podName
}
