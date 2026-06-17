package pg

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps"
	"github.com/sqc157400661/util"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"
)

type InstanceStepManager struct {
	steps.InstanceStepManager
}

// SetGlobalConfig is optional for standalone PostgreSQL instances because the
// initial PG path can use the images and runtime settings declared directly on
// KDBInstance.spec.instance.
func (s *InstanceStepManager) SetGlobalConfig() kube.BindFunc {
	return s.StepBinder(
		"SetPostgreSQLGlobalConfig",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			return flow.Pass()
		})
}

// SetInstanceConfig renders the minimal Patroni config used by PostgreSQL pods.
func (s *InstanceStepManager) SetInstanceConfig() kube.BindFunc {
	return s.StepBinder(
		"SetPostgreSQLInstanceConfig",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			instanceConfigMap := &corev1.ConfigMap{ObjectMeta: naming.InstanceConfigMap(instance)}
			instanceConfigMap.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))

			if err := errors.WithStack(rc.SetControllerReference(instanceConfigMap)); err != nil {
				return flow.Error(err, "set configmap reference err")
			}
			instanceConfigMap.Annotations = instance.Annotations
			instanceConfigMap.Labels = naming.Merge(instance.Labels, map[string]string{
				naming.LabelInstance: instance.Name,
			})

			patroni, err := buildPatroniConfig(instance)
			if err != nil {
				return flow.Error(err, "build patroni config err")
			}
			patroniBytes, err := yaml.Marshal(patroni)
			if err != nil {
				return flow.Error(err, "marshal patroni config err")
			}

			util.StringMap(&instanceConfigMap.Data)
			instanceConfigMap.Data[naming.PatroniConfigKey] = naming.YamlGeneratedWarning + string(patroniBytes)

			if err := errors.WithStack(rc.Apply(instanceConfigMap)); err != nil {
				return flow.Error(err, "apply postgresql configmap err")
			}
			rc.SetInstanceConfigMap(instanceConfigMap)
			return flow.Pass()
		})
}

func buildPatroniConfig(instance *v1.KDBInstance) (map[string]interface{}, error) {
	if instance == nil {
		return nil, fmt.Errorf("nil KDBInstance")
	}
	port := naming.KDBInstanceMasterPort(instance)
	if port == 0 {
		port = 5432
	}

	dcs := "kubernetes"
	ttl := int32(30)
	loopWait := int32(10)
	if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Patroni != nil {
		patroni := instance.Spec.PostgreSQL.Patroni
		if patroni.DCS != "" {
			dcs = patroni.DCS
		}
		if patroni.LeaderLeaseDurationSeconds != nil {
			ttl = *patroni.LeaderLeaseDurationSeconds
		}
		if patroni.SyncPeriodSeconds != nil {
			loopWait = *patroni.SyncPeriodSeconds
		}
	}
	if dcs != "kubernetes" {
		return nil, fmt.Errorf("postgresql patroni dcs %q is not implemented yet", dcs)
	}

	parameters := map[string]string{
		"archive_mode":             "on",
		"archive_command":          "pgbackrest --stanza=db archive-push %p",
		"hot_standby":              "on",
		"max_replication_slots":    "10",
		"max_wal_senders":          "10",
		"shared_preload_libraries": "pg_stat_statements",
		"wal_level":                "replica",
	}
	if instance.Spec.PostgreSQL != nil {
		for key, value := range instance.Spec.PostgreSQL.Parameters {
			if key == "data_directory" || key == "hba_file" || key == "port" {
				continue
			}
			parameters[key] = value
		}
	}

	hba := []string{
		"local all all trust",
		"host all all 0.0.0.0/0 md5",
		"host replication all 0.0.0.0/0 md5",
	}
	if instance.Spec.PostgreSQL != nil {
		hba = append(hba, instance.Spec.PostgreSQL.HBA...)
	}

	return map[string]interface{}{
		"scope":     instance.Name,
		"namespace": instance.Namespace,
		"name":      fmt.Sprintf("%s-${POD_NAME}", instance.Name),
		"restapi": map[string]interface{}{
			"listen":          "0.0.0.0:8008",
			"connect_address": "${POD_IP}:8008",
		},
		"kubernetes": map[string]interface{}{
			"namespace":   instance.Namespace,
			"scope_label": naming.LabelInstance,
			"role_label":  naming.LabelRole,
			"labels": map[string]string{
				naming.LabelInstance: instance.Name,
			},
			"use_endpoints": true,
		},
		"bootstrap": map[string]interface{}{
			"dcs": map[string]interface{}{
				"ttl":       ttl,
				"loop_wait": loopWait,
				"postgresql": map[string]interface{}{
					"use_slots":  true,
					"parameters": parameters,
					"pg_hba":     hba,
				},
			},
		},
		"postgresql": map[string]interface{}{
			"listen":          fmt.Sprintf("0.0.0.0:%d", port),
			"connect_address": fmt.Sprintf("${POD_IP}:%d", port),
			"data_dir":        fmt.Sprintf("%s/pg%s", naming.PostgreSQLDataMountPath, instance.Spec.EngineVersion),
			"bin_dir":         fmt.Sprintf("/usr/lib/postgresql/%s/bin", instance.Spec.EngineVersion),
			"authentication": map[string]interface{}{
				"superuser": map[string]string{
					"username": "postgres",
					"password": "postgres",
				},
				"replication": map[string]string{
					"username": "replicator",
					"password": "replicator",
				},
			},
			"parameters": map[string]string{
				"unix_socket_directories": naming.PostgreSQLSocketDirectory,
			},
		},
	}, nil
}
