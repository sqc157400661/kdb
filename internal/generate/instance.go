package generate

import (
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/config"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/util"
)

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

	instanceSet := v1.KDBInstanceSpec{
		InstanceSet: shared.InstanceSetSpec{
			Replicas:    desc.Replicas,
			Affinity:    desc.Affinity,
			Tolerations: desc.Tolerations,
			InitContainer: shared.ContainerSpec{
				Image: sidecarImage,
			},
			MainContainer: shared.ContainerSpec{
				Image: mainImage,
			},
			SidecarContainer: shared.ContainerSpec{
				Image: sidecarImage,
			},
			MonitorContainer: shared.ContainerSpec{
				Image: monitorImage,
			},
			DataVolumeClaimSpec: shared.PVCSpec{
				StorageClass: desc.StorageClass,
				Size:         desc.Size,
			},
		},
		Leader:            cluster.Spec.Leader,
		Port:              util.Int32(config.GetPortByEngine(cluster.Spec.Engine)),
		DeployArch:        cluster.Spec.DeployArch,
		Engine:            cluster.Spec.Engine,
		EngineFullVersion: desc.EngineFullVersion,
		Config:            globalConfig.GetDBConfig(cluster.Spec.Engine, desc.EngineFullVersion),
	}
	if !desc.LogSize.IsZero() {
		instanceSet.InstanceSet.LogVolumeClaimSpec = &shared.PVCSpec{
			Size:         desc.LogSize,
			StorageClass: desc.StorageClass,
		}
	}
	return nil
}
