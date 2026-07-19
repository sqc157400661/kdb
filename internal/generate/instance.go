package generate

import (
	"fmt"
	"github.com/sqc157400661/util"
	corev1 "k8s.io/api/core/v1"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/topology"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
)

// InitKDBInstance materializes a KDBInstance spec from cluster intent and instance descriptor.
//
// Leader/upstream is derived directly from ClusterPlan + current instance identity, so no
// secondary leader selection is performed in generate layer.
func InitKDBInstance(rc *context.ClusterContext, instance *v1.KDBInstance, desc *v1.InstanceDesc) error {
	cluster := rc.GetCluster()
	globalConfig := rc.GetGlobalConfig()
	instance.Labels = naming.Merge(instance.GetLabels(), cluster.GetLabels())
	instance.Annotations = naming.Merge(instance.GetAnnotations(), cluster.GetAnnotations())
	instance.Name = desc.Name
	mainImage, err := globalConfig.GetMainImage(cluster.Spec.Engine, desc.EngineFullVersion)
	if err != nil {
		return err
	}
	sidecarImage, err := globalConfig.GetSidecarImage(cluster.Spec.Engine, desc.EngineFullVersion)
	if err != nil {
		return err
	}
	monitorImage, err := globalConfig.GetMonitorImage(cluster.Spec.Engine, desc.EngineFullVersion)
	if err != nil {
		return err
	}
	clusterPlan, err := topology.ResolveClusterPlan(cluster)
	if err != nil {
		return err
	}
	selfIndex := -1
	for i := range cluster.Spec.Instances {
		if cluster.Spec.Instances[i].Name == desc.Name {
			selfIndex = i
			break
		}
	}
	if selfIndex < 0 {
		return fmt.Errorf("instance %s not found in cluster spec", desc.Name)
	}
	leaderIndex := clusterPlan.PrimaryIndex
	if clusterPlan.DeployArch == naming.MySQLMasterSlaveDeployArch {
		if selfIndex == clusterPlan.PrimaryIndex {
			leaderIndex = clusterPlan.PeerIndex
		}
	}
	master := v1.HostInfo{
		PodName: naming.InstancePodName(cluster.Spec.Instances[leaderIndex].Name, 0),
	}
	mainCommand, sidecarCommand, sidecarArgs := instanceRuntimeCommands(cluster.Spec.Engine)
	instanceSet := v1.KDBInstanceSpec{
		InstanceSet: shared.InstanceSetSpec{
			Replicas:    desc.Replicas,
			Affinity:    desc.Affinity,
			Tolerations: desc.Tolerations,
			InitContainer: shared.ContainerSpec{
				Image:     sidecarImage,
				Resources: desc.Resources,
			},
			MainContainer: shared.ContainerSpec{
				Image:     mainImage,
				Resources: desc.Resources,
				Command:   mainCommand, // TODO: format non-PostgreSQL engines to /kdb/bin/start.sh
			},
			SidecarContainer: shared.ContainerSpec{
				Image:   sidecarImage,
				Command: sidecarCommand,
				Args:    sidecarArgs,
				Resources: corev1.ResourceRequirements{
					Requests: util.GenerateResource(0.1, 0.5),
					Limits:   util.GenerateResource(0.1, 0.5),
				},
			},
			MonitorContainer: shared.ContainerSpec{
				Image:   monitorImage,
				Command: []string{"/kdb/bin/start.sh"},
				Resources: corev1.ResourceRequirements{
					Requests: util.GenerateResource(0.1, 0.5),
					Limits:   util.GenerateResource(0.1, 0.5),
				},
			},
			DataVolumeClaimSpec: shared.PVCSpec{
				StorageClass: desc.StorageClass,
				Size:         desc.Size,
			},
		},
		Leader:            master,
		Port:              util.Int32(naming.GetPortByEngine(cluster.Spec.Engine)),
		DeployArch:        cluster.Spec.DeployArch,
		Engine:            cluster.Spec.Engine,
		EngineVersion:     cluster.Spec.EngineVersion,
		EngineFullVersion: desc.EngineFullVersion,
		MySQL:             cloneClusterMySQLSpec(cluster),
		Config:            globalConfig.GetDBConfig(cluster.Spec.Engine, desc.EngineFullVersion),
	}
	if !desc.LogSize.IsZero() {
		instanceSet.InstanceSet.LogVolumeClaimSpec = &shared.PVCSpec{
			Size:         desc.LogSize,
			StorageClass: desc.StorageClass,
		}
	}
	instance.Spec = instanceSet
	return nil
}

func instanceRuntimeCommands(engine string) (main, sidecar, sidecarArgs []string) {
	if engine == naming.PostgresEngine || engine == naming.PostgresEnginePG {
		return []string{"kdb-pg-runtime"},
			[]string{"kdb-ha"},
			[]string{naming.PatroniConfigMountPath + "/" + naming.PatroniConfigKey}
	}
	return []string{"/bin/bash", "-c", "/kdb/bin/run_supervisor.sh"}, []string{"/kdb/bin/start.sh"}, nil
}

func cloneClusterMySQLSpec(cluster *v1.KDBCluster) *v1.MySQLSpec {
	if cluster == nil || cluster.Spec.MySQL == nil {
		return nil
	}
	out := &v1.MySQLSpec{}
	if cluster.Spec.MySQL.MGR != nil {
		mgr := *cluster.Spec.MySQL.MGR
		if cluster.Spec.DeployArch == naming.MySQLMGRDeployArch && mgr.GroupName == "" {
			mgr.GroupName = topology.StableMGRGroupName(cluster.Namespace, cluster.Name)
		}
		out.MGR = &mgr
	}
	if cluster.Spec.MySQL.Exporter != nil {
		exporter := *cluster.Spec.MySQL.Exporter
		if cluster.Spec.MySQL.Exporter.Env != nil {
			exporter.Env = append([]corev1.EnvVar(nil), cluster.Spec.MySQL.Exporter.Env...)
		}
		exporter.Resources = *cluster.Spec.MySQL.Exporter.Resources.DeepCopy()
		out.Exporter = &exporter
	}
	return out
}
