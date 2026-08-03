package clickhouse

import (
	"fmt"
	"time"

	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	internalconfig "github.com/sqc157400661/kdb/internal/config"
	internalsecurity "github.com/sqc157400661/kdb/internal/security"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ steps.InstanceStepper = (*InstanceStepManager)(nil)

type InstanceStepManager struct {
	steps.InstanceStepManager
}

func (s *InstanceStepManager) clickHouseNoOpStep(name string) kube.BindFunc {
	return s.StepBinder(
		name,
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			return flow.Pass()
		})
}

func (s *InstanceStepManager) SetGlobalConfig() kube.BindFunc {
	return s.StepBinder(
		"ClickHouseSetGlobalConfig",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			// Keep the ClickHouse-specific wrapper safe for unit-test and dry-run
			// callers that do not have an initialized reconcile context yet.
			if rc == nil {
				return flow.Pass()
			}
			if !clickHouseUsesStorageProfiles(rc.GetInstance()) {
				return flow.Pass()
			}
			step := kube.ExtractStepsFromBindFunc(s.InstanceStepManager.SetGlobalConfig())[0]
			result, err := step.Execute(rc, flow)
			if err != nil || result.Requeue || result.RequeueAfter > 0 {
				return result, err
			}
			if err = resolveClickHouseStorageProfiles(rc.GetInstance(), rc.GetGlobalConfig()); err != nil {
				return flow.Error(err, "resolve clickhouse storage profiles err")
			}
			return flow.Pass()
		})
}

func clickHouseUsesStorageProfiles(instance *v1.KDBInstance) bool {
	if instance == nil || instance.Spec.ClickHouse == nil {
		return false
	}
	if instance.Spec.ClickHouse.StorageProfileRef != "" {
		return true
	}
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		if group.StorageProfileRef != "" {
			return true
		}
	}
	return false
}

func resolveClickHouseStorageProfiles(instance *v1.KDBInstance, global internalconfig.GlobalConfig) error {
	resolve := func(ref string, target *shared.InstanceSetSpec, owner string, replicas int32) error {
		if ref == "" {
			return nil
		}
		profile, ok := global.StorageProfiles[ref]
		if !ok {
			return fmt.Errorf("storage profile %q for %s is not registered", ref, owner)
		}
		if profile.StorageClass == "" {
			return fmt.Errorf("storage profile %q has no storage_class", ref)
		}
		if profile.Local && replicas < 2 {
			return fmt.Errorf("local storage profile %q requires at least two replicas per shard", ref)
		}
		target.DataVolumeClaimSpec.StorageClass = profile.StorageClass
		if profile.Local {
			if target.Metadata == nil {
				target.Metadata = &shared.Metadata{}
			}
			if target.Metadata.Annotations == nil {
				target.Metadata.Annotations = map[string]string{}
			}
			target.Metadata.Annotations[annotationRequireCrossNodeReplicas] = "true"
		} else if target.Metadata != nil && target.Metadata.Annotations != nil {
			delete(target.Metadata.Annotations, annotationRequireCrossNodeReplicas)
		}
		return nil
	}
	defaultRef := instance.Spec.ClickHouse.StorageProfileRef
	for i := range instance.Spec.ClickHouse.ComputeGroups {
		group := &instance.Spec.ClickHouse.ComputeGroups[i]
		ref := group.StorageProfileRef
		if ref == "" {
			ref = defaultRef
		}
		if err := resolve(ref, &group.Instance, "compute group "+group.Name, replicasPerShard(*group)); err != nil {
			return err
		}
	}
	if instance.Spec.ClickHouse.Keeper.Instance != nil {
		keeperReplicas := int32(3)
		if instance.Spec.ClickHouse.Keeper.Replicas != nil {
			keeperReplicas = *instance.Spec.ClickHouse.Keeper.Replicas
		}
		if err := resolve(defaultRef, instance.Spec.ClickHouse.Keeper.Instance, "keeper", keeperReplicas); err != nil {
			return err
		}
	}
	return nil
}

func (s *InstanceStepManager) EnsureLeader() kube.BindFunc {
	return s.clickHouseNoOpStep("ClickHouseEnsureLeaderNoOp")
}

func (s *InstanceStepManager) HandleDelete() kube.BindFunc {
	return s.StepBinder(
		"ClickHouseProtectedDelete",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			if !rc.IsDeleting() {
				return flow.Pass()
			}
			if err := protectedDeleteAllowed(rc.GetInstance()); err != nil {
				return flow.Error(err, "clickhouse protected deletion blocked")
			}
			ready, err := prepareClickHouseDeletion(rc)
			if err != nil {
				return flow.Error(err, "prepare clickhouse protected deletion err")
			}
			if !ready {
				return flow.RetryAfter(10*time.Second, "clickhouse protected deletion in progress")
			}
			step := kube.ExtractStepsFromBindFunc(s.InstanceStepManager.HandleDelete())[0]
			return step.Execute(rc, flow)
		})
}

func (s *InstanceStepManager) SetInstanceConfig() kube.BindFunc {
	return s.StepBinder(
		"ClickHouseSetInstanceConfig",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			configMaps, err := buildClickHouseConfigMaps(rc.GetInstance())
			if err != nil {
				return flow.Error(err, "build clickhouse configmap err")
			}
			for _, configMap := range configMaps {
				if err = errors.WithStack(rc.SetControllerReference(configMap)); err != nil {
					return flow.Error(err, "set clickhouse configmap reference err")
				}
				if err = errors.WithStack(rc.Apply(configMap)); err != nil {
					return flow.Error(err, "apply clickhouse configmap err")
				}
			}
			if len(configMaps) > 0 {
				rc.SetInstanceConfigMap(configMaps[0])
			}

			secret, err := buildStandaloneSecret(rc.GetInstance())
			if err != nil {
				return flow.Error(err, "build clickhouse secret err")
			}
			existingSecret := &corev1.Secret{}
			err = rc.Client().Get(rc.Context(), types.NamespacedName{Namespace: secret.Namespace, Name: secret.Name}, existingSecret)
			switch {
			case err == nil:
				generated, generateErr := newClickHouseCredentialData()
				if generateErr != nil {
					return flow.Error(generateErr, "generate clickhouse credentials err")
				}
				secret.Data = existingSecret.Data
				if secret.Data == nil {
					secret.Data = map[string][]byte{}
				}
				for key, value := range generated {
					if len(secret.Data[key]) == 0 {
						secret.Data[key] = value
					}
				}
				secret.Data["admin-username"] = generated["admin-username"]
			case apierrors.IsNotFound(err):
				secret.Data, err = newClickHouseCredentialData()
				if err != nil {
					return flow.Error(err, "generate clickhouse credentials err")
				}
			default:
				return flow.Error(err, "get existing clickhouse secret err")
			}
			if len(secret.Data["ca.crt"]) == 0 || len(secret.Data["tls.crt"]) == 0 || len(secret.Data["tls.key"]) == 0 ||
				len(secret.Data["client.crt"]) == 0 || len(secret.Data["client.key"]) == 0 {
				bundle, tlsErr := internalsecurity.GenerateRuntimeMTLSBundle(rc.GetInstance(), "ClickHouse sidecar")
				if tlsErr != nil {
					return flow.Error(tlsErr, "generate clickhouse sidecar TLS identity err")
				}
				secret.Data["ca.crt"] = bundle.CA
				secret.Data["tls.crt"], secret.Data["tls.key"] = bundle.ServerCert, bundle.ServerKey
				secret.Data["client.crt"], secret.Data["client.key"] = bundle.ClientCert, bundle.ClientKey
			}
			if err = errors.WithStack(rc.SetControllerReference(secret)); err != nil {
				return flow.Error(err, "set clickhouse secret reference err")
			}
			if err = errors.WithStack(rc.Apply(secret)); err != nil {
				return flow.Error(err, "apply clickhouse secret err")
			}
			keeperConfigMaps, err := buildKeeperConfigMaps(rc.GetInstance())
			if err != nil {
				return flow.Error(err, "build clickhouse keeper configmaps err")
			}
			for _, configMap := range keeperConfigMaps {
				if err = errors.WithStack(rc.SetControllerReference(configMap)); err != nil {
					return flow.Error(err, "set clickhouse keeper configmap reference err")
				}
				if err = errors.WithStack(rc.Apply(configMap)); err != nil {
					return flow.Error(err, "apply clickhouse keeper configmap err")
				}
			}
			return flow.Pass()
		})
}

func (s *InstanceStepManager) SetRbac() kube.BindFunc {
	return s.clickHouseNoOpStep("ClickHouseSetRbacNoOp")
}

func (s *InstanceStepManager) SetService() kube.BindFunc {
	return s.StepBinder(
		"ClickHouseSetService",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			services, err := buildClickHouseServices(rc.GetInstance())
			if err != nil {
				return flow.Error(err, "build clickhouse services err")
			}
			for _, service := range services {
				if err = errors.WithStack(rc.SetControllerReference(service)); err != nil {
					return flow.Error(err, "set clickhouse service reference err")
				}
				if err = errors.WithStack(rc.Apply(service)); err != nil {
					return flow.Error(err, "apply clickhouse service err")
				}
			}
			if len(services) > 0 {
				rc.SetInstancePodService(services[0])
			}
			keeperService, err := buildKeeperHeadlessService(rc.GetInstance())
			if err != nil {
				return flow.Error(err, "build clickhouse keeper service err")
			}
			if keeperService != nil {
				if err = errors.WithStack(rc.SetControllerReference(keeperService)); err != nil {
					return flow.Error(err, "set clickhouse keeper service reference err")
				}
				if err = errors.WithStack(rc.Apply(keeperService)); err != nil {
					return flow.Error(err, "apply clickhouse keeper service err")
				}
			}
			return flow.Pass()
		})
}

func (s *InstanceStepManager) InitObservedRunner() kube.BindFunc {
	return s.StepBinder(
		"ClickHouseProjectStandaloneStatus",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			if err := projectStandaloneStatus(rc); err != nil {
				return flow.Error(err, "project clickhouse status err")
			}
			return flow.Pass()
		})
}

func (s *InstanceStepManager) ScaleUpInstance() kube.BindFunc {
	return s.StepBinder(
		"ClickHouseScaleUpHosts",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			if err := validateObservedClickHouseChanges(rc); err != nil {
				return flow.Error(err, "validate observed clickhouse topology change err")
			}
			if err := reconcileClickHousePVCExpansion(rc); err != nil {
				return flow.Error(err, "expand clickhouse pvc err")
			}
			if err := reconcileKeeperStatefulSets(rc); err != nil {
				return flow.Error(err, "reconcile clickhouse keeper statefulsets err")
			}
			keeperRolling, err := reconcileNextKeeperRollout(rc)
			if err != nil {
				return flow.Error(err, "roll clickhouse keeper member err")
			}
			if keeperRolling {
				return flow.RetryAfter(10*time.Second, "clickhouse Keeper rollout in progress")
			}
			result, ready, err := gateClickHouseOnKeeper(rc)
			if err != nil {
				return flow.Error(err, "check clickhouse keeper readiness err")
			}
			if !ready {
				return result, nil
			}
			statefulSets, err := buildClickHouseStatefulSets(rc.GetInstance(), rc.GetClusterServiceAccountName())
			if err != nil {
				return flow.Error(err, "build clickhouse statefulsets err")
			}
			backupRevision, err := clickHouseBackupConfigRevision(rc)
			if err != nil {
				return flow.Error(err, "read clickhouse backup configuration secret err")
			}
			credentialRevision, err := clickHouseCredentialRevision(rc)
			if err != nil {
				return flow.Error(err, "read clickhouse credential secret err")
			}
			for _, statefulSet := range statefulSets {
				statefulSet.Spec.Template.Annotations[annotationBackupConfigRevision] = backupRevision
				statefulSet.Spec.Template.Annotations[annotationCredentialRevision] = credentialRevision
				if err = preserveStatefulSetClaimTemplates(rc, statefulSet); err != nil {
					return flow.Error(err, "preserve clickhouse statefulset claim templates err")
				}
				if err = errors.WithStack(rc.SetControllerReference(statefulSet)); err != nil {
					return flow.Error(err, "set clickhouse statefulset reference err")
				}
				if err = errors.WithStack(rc.Apply(statefulSet)); err != nil {
					return flow.Error(err, "apply clickhouse statefulset err")
				}
			}
			return flow.Pass()
		})
}

func (s *InstanceStepManager) ScaleDownInstance() kube.BindFunc {
	return s.StepBinder(
		"ClickHouseScaleDownAndRolling",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			if rc == nil {
				return flow.Pass()
			}
			requeue, err := reconcileClickHouseScaleDownAndRolling(rc)
			if err != nil {
				return flow.Error(err, "reconcile clickhouse scale down or rolling update err")
			}
			if requeue {
				return flow.RetryAfter(clickHouseRolloutRequeue(), "clickhouse rollout in progress")
			}
			return flow.Pass()
		})
}

func (s *InstanceStepManager) ReconcileProxySQL() kube.BindFunc {
	return s.StepBinder(
		"ClickHouseReconcileGateway",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			if err := reconcileGateway(rc); err != nil {
				return flow.Error(err, "reconcile clickhouse gateway err")
			}
			return flow.RetryAfter(15*time.Second, "refresh clickhouse sidecar and replication status")
		})
}
