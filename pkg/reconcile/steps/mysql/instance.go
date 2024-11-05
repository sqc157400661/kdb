package mysql

import (
	"github.com/hashicorp/go-version"
	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	"github.com/sqc157400661/kdb/internal/config"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps"
	"github.com/sqc157400661/util"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type InstanceStepManager struct {
	steps.InstanceStepManager
}

// SetInstanceConfig set mysql and sidecar config
// TODO: processing parameters that require a restart to take effect
func (s *InstanceStepManager) SetInstanceConfig() kube.BindFunc {
	return s.StepBinder(
		"SetInstanceConfig",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			instanceConfigMap := &corev1.ConfigMap{ObjectMeta: naming.InstanceConfigMap(instance)}
			instanceConfigMap.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))

			err := errors.WithStack(rc.SetControllerReference(instanceConfigMap))

			if err != nil {
				return flow.Error(err, "Set Reference err")
			}
			instanceConfigMap.Annotations = instance.Annotations
			instanceConfigMap.Labels = naming.Merge(instance.Labels,
				map[string]string{
					naming.LabelInstance: instance.Name,
				})
			globalConfig := rc.GetGlobalConfig()
			// create config
			util.StringMap(&instanceConfigMap.Data)
			configStr, err := util.SafeTemplateFill(config.InstanceConfigTmpl, map[string]interface{}{
				"RootUser":       globalConfig.DB.RootUser,
				"RootPassword":   globalConfig.DB.RootPassword,
				"ReplUser":       globalConfig.DB.ReplUser,
				"ReplPassword":   globalConfig.DB.ReplPassword,
				"MasterIP":       naming.KDBInstanceMasterHostname(instance),
				"MasterHostname": naming.KDBInstanceMasterIp(instance),
			})
			if err != nil {
				return flow.Error(err, "get instance config err")
			}
			if err != nil {
				return flow.Error(err, "conf create err")
			}
			instanceConfigMap.Data[naming.SidecarConfigKey] = naming.YamlGeneratedWarning + configStr
			v1, err := naming.EngineVersion(instance)
			if err != nil {
				return flow.Error(err, "get instance version err")
			}
			v2, _ := version.NewVersion("8.0")
			// TODO: 根据内存和cpu动态调整 tmpl配置的内容
			if v1.GreaterThanOrEqual(v2) {
				instanceConfigMap.Data[naming.DatabaseConfigKey] = naming.YamlGeneratedWarning + config.MySQL8ConfTmpl
			} else {
				instanceConfigMap.Data[naming.DatabaseConfigKey] = naming.YamlGeneratedWarning + config.MySQL57ConfTmpl
			}
			err = errors.WithStack(rc.Apply(instanceConfigMap))
			if err != nil {
				return flow.Error(err, "apply err")
			}
			rc.SetInstanceConfigMap(instanceConfigMap)
			return flow.Pass()
		})
}
