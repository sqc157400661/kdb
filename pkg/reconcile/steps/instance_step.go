package steps

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/config"
	internalmonitoring "github.com/sqc157400661/kdb/internal/monitoring"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/observed"
	"github.com/sqc157400661/kdb/internal/postgresqlruntime"
	"github.com/sqc157400661/kdb/internal/rbac"
	"github.com/sqc157400661/kdb/internal/topology"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
)

type ConditionFunc func(rc *context.InstanceContext, log logr.Logger) (bool, error)
type StepFunc func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error)

type InstanceStepper interface {
	StepBinder(name string, f StepFunc) kube.BindFunc
	StepIfBinder(conditionName string, condFunc ConditionFunc, binders ...kube.BindFunc) kube.BindFunc
	PatchKDBInstanceStatus() kube.BindFunc
	PatchKDBInstance() kube.BindFunc
	CheckAndSetFinalizer() kube.BindFunc
	HandleDelete() kube.BindFunc
	SetGlobalConfig() kube.BindFunc
	EnsureLeader() kube.BindFunc
	ReconcileLifecycle() kube.BindFunc
	SetInstanceConfig() kube.BindFunc
	SetRbac() kube.BindFunc
	InitObservedRunner() kube.BindFunc
	SetService() kube.BindFunc
	ScaleUpInstance() kube.BindFunc
	ScaleDownInstance() kube.BindFunc
	SetMonitor() kube.BindFunc
	ReconcileProxySQL() kube.BindFunc
}

func (s *InstanceStepManager) ReconcileLifecycle() kube.BindFunc {
	return s.StepBinder("ReconcileLifecycle", func(_ *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) { return flow.Pass() })
}

type InstanceStepManager struct {
}

// StepBinder bind one step to a task function
func (s *InstanceStepManager) StepBinder(name string, f StepFunc) kube.BindFunc {
	return kube.NewStepBinder(
		kube.NewStep(
			name, func(rc kube.ReconcileContext, flow kube.Flow) (reconcile.Result, error) {
				return f(rc.(*context.InstanceContext), flow)
			},
		),
	)
}

// StepIfBinder bind one condition step to a task function
func (s *InstanceStepManager) StepIfBinder(conditionName string, condFunc ConditionFunc, binders ...kube.BindFunc) kube.BindFunc {
	condition := kube.NewCachedCondition(
		kube.NewCondition(conditionName, func(rc kube.ReconcileContext, log logr.Logger) (bool, error) {
			return condFunc(rc.(*context.InstanceContext), log)
		}),
	)

	ifBinders := make([]kube.BindFunc, len(binders))
	for i := range binders {
		ifBinders[i] = kube.NewStepIfBinder(condition, kube.ExtractStepsFromBindFunc(binders[i])[0])
	}

	return kube.CombineBinders(ifBinders...)
}

// PatchKDBInstanceStatus patch instance status
func (s *InstanceStepManager) PatchKDBInstanceStatus() kube.BindFunc {
	return s.StepBinder(
		"PatchKDBInstanceStatus",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			err := rc.PatchKDBInstanceStatus()
			if err != nil {
				return flow.Error(err, "patch mysql instance Status err")
			}
			return flow.Pass()
		})
}

// PatchKDBInstance patch instance
func (s *InstanceStepManager) PatchKDBInstance() kube.BindFunc {
	return s.StepBinder(
		"PatchKDBInstance",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			err := rc.PatchKDBInstance()
			if err != nil {
				return flow.Error(err, "patch mysql instance err")
			}
			return flow.Pass()
		})
}

// CheckAndSetFinalizer check if the Finalizer exists, if not, add it
func (s *InstanceStepManager) CheckAndSetFinalizer() kube.BindFunc {
	return s.StepBinder(
		"CheckAndSetFinalizer",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			if !rc.IsDeleted() && !rc.IsDeleting() {
				if rc.HasFinalizer(naming.Finalizer) {
					return flow.Pass()
				}
				// The cluster is not being deleted and needs a finalizer; set it.

				// The Finalizers field is shared by multiple controllers, but the
				// server-side merge strategy does not work on our custom resource due
				// to a bug in Kubernetes. Build a merge-patch that includes the full
				// list of Finalizers plus ResourceVersion to detect conflicts with
				// other potential writers.
				// - https://issue.k8s.io/99730
				before := rc.GetInstance().DeepCopy()
				// Make another copy so that Patch doesn't write back to cluster.
				intent := before.DeepCopy()
				intent.Finalizers = append(intent.Finalizers, naming.Finalizer)
				err := errors.WithStack(rc.Patch(intent,
					client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})))
				if err != nil {
					return flow.Error(err, "patch finalizers error")
				}
			}
			return flow.Pass()
		})
}

// HandleDelete sets a finalizer on cluster and performs the finalization of
// cluster when it is being deleted. It returns (nil, nil) when cluster is
// not being deleted. The caller is responsible for returning other values to
// controller-runtime.
func (s *InstanceStepManager) HandleDelete() kube.BindFunc {
	return s.StepBinder(
		"HandleDelete",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			// Find all instance pods to determine which to shut down and in what order.
			pods := &corev1.PodList{}
			instances, err := naming.AsSelector(naming.KDBInstance(rc.Name()))
			if err == nil {
				err = errors.WithStack(rc.List(pods, instances))
			}
			if err != nil {
				return flow.Error(err, "get pod list err")
			}

			if len(pods.Items) == 0 {
				// TODO: Remove to the cluster cr?
				// Instances are stopped, now cleanup some haproxy stuff.
				// as haproxy creates them. Would their events cause too many reconciles?
				// Foreground deletion may force us to adopt and set finalizers anyway.
				var selector labels.Selector
				selector, err = naming.AsSelector(naming.KDBInstanceHaProxy(rc.GetInstance()))
				if err == nil {
					err = errors.WithStack(
						rc.Client().DeleteAllOf(rc.Context(), &corev1.Endpoints{},
							client.InNamespace(rc.Namespace()),
							client.MatchingLabelsSelector{Selector: selector},
						))
				}
				if err != nil {
					return flow.Error(err, "delete haproxy stuff err")
				}
				// Our finalizer logic is finished; remove our finalizer.
				// The Finalizers field is shared by multiple controllers, but the
				// server-side merge strategy does not work on our custom resource due to a
				// bug in Kubernetes. Build a merge-patch that includes the full list of
				// Finalizers plus ResourceVersion to detect conflicts with other potential
				// writers.
				// - https://issue.k8s.io/99730
				before := rc.GetInstance().DeepCopy()
				// Make another copy so that Patch doesn't write back to cluster.
				intent := before.DeepCopy()
				intent.Finalizers = rc.DeleteFinalizer(naming.Finalizer)
				err = errors.WithStack(rc.Patch(intent,
					client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})))
				if err != nil {
					return flow.Error(err, "patch finalizers error")
				}
				// The caller should wait for further events or requeue upon error.
				return flow.Continue("deleted")
			}

			jobControllerDeleted := errors.New("one-shot Job controller deleted")
			// stop schedules pod for deletion by scaling its controller to zero.
			stop := func(pod *corev1.Pod) error {
				instance := &unstructured.Unstructured{}
				instance.SetNamespace(rc.Namespace())
				deleteController := false

				switch owner := metav1.GetControllerOfNoCopy(pod); {
				case owner == nil:
					if pod.Labels[naming.LabelInstance] == rc.Name() {
						if err := rc.Client().Delete(rc.Context(), pod); client.IgnoreNotFound(err) != nil {
							return errors.WithStack(err)
						}
						return jobControllerDeleted
					}
					return errors.Errorf("pod %q has no owner", client.ObjectKeyFromObject(pod))

				case owner.Kind == "StatefulSet":
					instance.SetAPIVersion(owner.APIVersion)
					instance.SetKind(owner.Kind)
					instance.SetName(owner.Name)

				case owner.Kind == "Deployment":
					instance.SetAPIVersion(owner.APIVersion)
					instance.SetKind(owner.Kind)
					instance.SetName(owner.Name)

				case owner.Kind == "Job":
					// One-shot bootstrap/configuration Jobs have no replicas field.
					// Delete the owning Job so finalization can continue safely.
					instance.SetAPIVersion(owner.APIVersion)
					instance.SetKind(owner.Kind)
					instance.SetName(owner.Name)
					deleteController = true

				case owner.Kind == "ReplicaSet":
					// Deployment Pods are controlled indirectly through a
					// ReplicaSet. Scale the Deployment itself so it cannot
					// immediately recreate a PgBouncer Pod during finalization.
					replicaSet := &appsv1.ReplicaSet{}
					replicaSet.Namespace, replicaSet.Name = rc.Namespace(), owner.Name
					if err := rc.Get(replicaSet); err != nil {
						return errors.WithStack(err)
					}
					deploymentOwner := metav1.GetControllerOfNoCopy(replicaSet)
					if deploymentOwner != nil && deploymentOwner.Kind == "Deployment" {
						instance.SetAPIVersion(deploymentOwner.APIVersion)
						instance.SetKind(deploymentOwner.Kind)
						instance.SetName(deploymentOwner.Name)
					} else {
						instance.SetAPIVersion(owner.APIVersion)
						instance.SetKind(owner.Kind)
						instance.SetName(owner.Name)
					}

				default:
					return errors.Errorf("unexpected kind %q", owner.Kind)
				}

				if deleteController {
					if err := rc.Client().Delete(rc.Context(), instance); client.IgnoreNotFound(err) != nil {
						return errors.WithStack(err)
					}
					return jobControllerDeleted
				}

				// apps/v1.Deployment, apps/v1.ReplicaSet, and apps/v1.StatefulSet all
				// have a "spec.replicas" field with the same meaning.
				patch := client.RawPatch(client.Merge.Type(), []byte(`{"spec":{"replicas":0}}`))
				sErr := errors.WithStack(rc.Patch(instance, patch))

				return sErr
			}

			if len(pods.Items) == 1 {
				// There's one instance; stop it.
				if err = stop(&pods.Items[0]); err != nil {
					if errors.Is(err, jobControllerDeleted) {
						return flow.RetryAfter(time.Second, "waiting for one-shot Job Pod deletion")
					}
					if client.IgnoreNotFound(err) != nil {
						return flow.RetryErr(err, err.Error())
					}
					// When the pod controller is missing, requeue rather than return an
					// error. The garbage collector will stop the pod, and it is not our
					// mistake that something else is deleting objects. Use RequeueAfter to
					// avoid being rate-limited due to a deluge of delete events.
					return flow.RetryAfter(10*time.Second, "")
				}
				return flow.Break("deleting")
			}

			// There are multiple instances; stop the replicas. When none are found,
			// requeue to try again.
			requeue := true
			for i := range pods.Items {
				role := pods.Items[i].Labels[naming.LabelRole]
				if role == naming.ReplicaRole || len(role) == 0 {
					if err = stop(&pods.Items[i]); err != nil {
						if errors.Is(err, jobControllerDeleted) {
							return flow.RetryAfter(time.Second, "waiting for one-shot Job Pod deletion")
						}
						if client.IgnoreNotFound(err) != nil {
							return flow.RetryErr(err, err.Error())
						}
						return flow.RetryAfter(10*time.Second, "")
					}
					requeue = false
				}
			}
			if requeue {
				return flow.Retry("Retry")
			}
			return flow.Break("deleting")
		})
}

// SetGlobalConfig including configuration information such as Root certificate, username and password
func (s *InstanceStepManager) SetGlobalConfig() kube.BindFunc {
	return s.StepBinder(
		"SetGlobalConfig",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			existing := &corev1.Secret{}
			existing.Namespace, existing.Name = instance.Namespace, naming.GlobalConfigSecret
			err := errors.WithStack(client.IgnoreNotFound(rc.Get(existing)))
			if err != nil {
				return flow.Error(err, "get GlobalConfig err")
			}
			if len(existing.Data) == 0 {
				return flow.Error(errors.New("GlobalConfig not exist"), "get GlobalConfig err")
			}
			globalConf := existing.Data[naming.GlobalConfigSecretKey]
			if len(globalConf) == 0 {
				return flow.Pass()
			}
			var conf config.GlobalConfig
			err = json.Unmarshal(globalConf, &conf)
			if err != nil {
				return flow.Error(errors.New("Unmarshal err"), err.Error())
			}
			rc.SetGlobalConfig(&conf)
			return flow.Pass()
		})
}

// EnsureLeader initializes spec.leader only for standalone KDBInstance usage.
//
// Cluster-managed instances are skipped because cluster reconciliation owns leader semantics.
// For standalone instances, the default is derived from topology to keep behavior consistent.
func (s *InstanceStepManager) EnsureLeader() kube.BindFunc {
	return s.StepBinder(
		"EnsureLeader",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			if instance == nil {
				return flow.Pass()
			}
			plan, err := topology.ResolveInstancePlan(instance)
			if err != nil {
				return flow.Error(err, "resolve topology plan err")
			}
			if naming.KDBInstanceClusterID(instance) != "" {
				return flow.Pass()
			}
			if instance.Spec.Leader.PodName != "" || instance.Spec.Leader.Host != "" {
				return flow.Pass()
			}
			instance.Spec.Leader = topology.LeaderForInstance(instance, plan, 0)
			return flow.Retry("leader initialized")
		})
}

func (s *InstanceStepManager) SetInstanceConfig() kube.BindFunc {
	return s.StepBinder(
		"SetInstanceConfig",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {

			return flow.Pass()
		})
}

func (s *InstanceStepManager) SetRbac() kube.BindFunc {
	return s.StepBinder(
		"SetRbacForInstancePod",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			account := &corev1.ServiceAccount{ObjectMeta: naming.InstanceRBAC(instance)}
			account.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ServiceAccount"))

			binding := &rbacv1.RoleBinding{ObjectMeta: naming.InstanceRBAC(instance)}
			binding.SetGroupVersionKind(rbacv1.SchemeGroupVersion.WithKind("RoleBinding"))

			role := &rbacv1.Role{ObjectMeta: naming.InstanceRBAC(instance)}
			role.SetGroupVersionKind(rbacv1.SchemeGroupVersion.WithKind("Role"))

			err := errors.WithStack(rc.SetControllerReference(account))
			if err == nil {
				err = errors.WithStack(rc.SetControllerReference(binding))
			}
			if err == nil {
				err = errors.WithStack(rc.SetControllerReference(role))
			}

			account.Annotations = instance.Annotations
			account.Labels = naming.Merge(instance.Labels,
				map[string]string{
					naming.LabelInstance: instance.Name,
				})
			binding.Annotations = instance.Annotations
			binding.Labels = naming.Merge(instance.Labels,
				map[string]string{
					naming.LabelInstance: instance.Name,
				})
			role.Annotations = instance.Annotations
			role.Labels = naming.Merge(instance.Labels,
				map[string]string{
					naming.LabelInstance: instance.Name,
				})

			account.AutomountServiceAccountToken = util.Bool(true)
			binding.RoleRef = rbacv1.RoleRef{
				APIGroup: rbacv1.SchemeGroupVersion.Group,
				Kind:     role.Kind,
				Name:     role.Name,
			}
			binding.Subjects = []rbacv1.Subject{{
				Kind:      account.Kind,
				Name:      account.Name,
				Namespace: instance.Namespace,
			}}
			role.Rules = rbac.KDBInstancePodPermissions()
			if naming.IsPGEngine(instance) {
				role.Rules = rbac.PostgreSQLInstancePodPermissions()
			}
			if err == nil {
				err = errors.WithStack(rc.Apply(account))
			}
			if err == nil {
				err = errors.WithStack(rc.Apply(role))
			}
			if err == nil {
				err = errors.WithStack(rc.Apply(binding))
			}

			if err != nil {
				return flow.Error(err, "create rbac err")
			}
			rc.SetClusterServiceAccount(account)
			return flow.Pass()
		})
}

func (s *InstanceStepManager) InitObservedRunner() kube.BindFunc {
	return s.StepBinder(
		"InitObservedInstances",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			pods := &corev1.PodList{}
			runners := &appsv1.StatefulSetList{}
			selector, err := naming.AsSelector(naming.KDBInstance(rc.Name()))
			if err != nil {
				return flow.Error(err, "get selector err")
			}
			err = rc.List(pods, selector)
			if err != nil {
				return flow.Error(err, "get pod list err")
			}
			err = rc.List(runners, selector)
			if err != nil {
				return flow.Error(err, "get runners list err")
			}
			obs := observed.NewObservedRunner(instance, runners.Items, pods.Items)
			rc.SetObservedRunner(obs)
			status := shared.InstanceSetStatus{}
			status.Replicas = *instance.Spec.InstanceSet.Replicas
			// Fill out status sorted by set name.
			for _, item := range obs.List {
				if item == nil || len(item.Pods) == 0 {
					continue
				}
				pod := item.Pods[0]
				if util.IsPodReady(pod) {
					status.ReadyReplicas++
					status.PodInfos = append(status.PodInfos, shared.PodStatusInfo{
						PodName:  pod.Name,
						PodUID:   string(pod.UID),
						PodPhase: pod.Status.Phase,
						PodIP:    pod.Status.PodIP,
						NodeName: pod.Spec.NodeName,
						HostIP:   pod.Status.HostIP,
					})
				}
				if matches, known := item.PodMatchesPodTemplate(); known && matches {
					status.UpdatedReplicas++
				}
			}
			instance.Status.InstanceSet = status
			return flow.Pass()
		})
}

func (s *InstanceStepManager) SetService() kube.BindFunc {
	return s.StepBinder(
		"SetService",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Namespace: instance.Namespace,
				Name:      naming.InstancePodServiceName(instance.Name),
			}}
			service.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
			if err := errors.WithStack(rc.SetControllerReference(service)); err != nil {
				return flow.Error(err, "set service controller ref err")
			}

			service.Annotations = instance.Annotations
			service.Labels = naming.Merge(instance.Labels, map[string]string{
				naming.LabelInstance: instance.Name,
			})
			service.Spec = corev1.ServiceSpec{
				ClusterIP: corev1.ClusterIPNone,
				Selector: map[string]string{
					naming.LabelInstance: instance.Name,
				},
				Ports: []corev1.ServicePort{{
					Name: naming.PortDatabase,
					Port: naming.KDBInstanceMasterPort(instance),
				}},
			}

			if err := errors.WithStack(rc.Apply(service)); err != nil {
				return flow.Error(err, "apply instance pod headless service err")
			}
			rc.SetInstancePodService(service)
			return flow.Pass()
		})
}

func (s *InstanceStepManager) ScaleUpInstance() kube.BindFunc {
	return s.StepBinder(
		"ScaleUpInstance",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			// Reconcile every desired StatefulSet, including existing sets, so
			// spec-only changes such as shutdown can update their pod replicas.
			var runners []*appsv1.StatefulSet
			desired := int(*instance.Spec.InstanceSet.Replicas)
			if naming.IsPGEngine(instance) && rc.GetObservedRunner() != nil {
				names := sets.NewString()
				for _, observed := range rc.GetObservedRunner().List {
					if observed == nil || observed.Runner == nil {
						continue
					}
					names.Insert(observed.Name)
					runners = append(runners, &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: observed.Name, Namespace: instance.Namespace}})
				}
				for ordinal := 0; len(runners) < desired; ordinal++ {
					next := naming.GenerateInstanceStatefulSetMeta(instance, ordinal)
					if names.Has(next.Name) {
						continue
					}
					names.Insert(next.Name)
					runners = append(runners, &appsv1.StatefulSet{ObjectMeta: next})
				}
			} else {
				for i := 0; i < desired; i++ {
					next := naming.GenerateInstanceStatefulSetMeta(instance, i)
					runners = append(runners, &appsv1.StatefulSet{ObjectMeta: next})
				}
			}
			var err error
			for n := range runners {
				if naming.IsMySQLEngine(instance) {
					err = reconcileMySQLInstance(rc, runners[n])
				} else if naming.IsPGEngine(instance) {
					err = reconcilePGInstance(rc, runners[n])
				}
				if err != nil {
					return flow.Error(err, "reconcileInstance err")
				}
			}

			return flow.Pass()
		})
}

func (s *InstanceStepManager) ScaleDownInstance() kube.BindFunc {
	return s.StepBinder(
		"ScaleDownInstance",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			if naming.IsMySQLEngine(rc.GetInstance()) {
				rolling, err := rolloutOutdatedPod(rc)
				if err != nil {
					return flow.Error(err, "roll out outdated mysql pod err")
				}
				if rolling {
					return flow.RetryAfter(5*time.Second, "mysql pod rollout in progress")
				}
			} else if naming.IsPGEngine(rc.GetInstance()) {
				if err := preparePostgreSQLCredentialRotation(rc); err != nil {
					return flow.Error(err, "prepare postgresql credential rotation err")
				}
				rolling, err := rolloutOutdatedPod(rc)
				if err != nil {
					return flow.Error(err, "roll out outdated postgresql pod err")
				}
				if rolling {
					return flow.RetryAfter(5*time.Second, "postgresql pod rollout in progress")
				}
			}
			observedInstances := rc.GetObservedRunner()
			namesToKeep := getNamesNeedToKeep(rc)
			for _, ins := range observedInstances.List {
				if !namesToKeep.Has(ins.Name) {
					err := deleteSts(rc, ins.Name)
					if err != nil {
						return flow.Error(err, "deleteInstance err")
					}
				}
			}
			return flow.Pass()
		})
}

// rolloutOutdatedPod advances an OnDelete StatefulSet update one ready Pod
// at a time. Replica Pods are replaced before the primary so enabling an
// exporter or changing another Pod-template field does not restart every MySQL
// member at once.
func rolloutOutdatedPod(rc *context.InstanceContext) (bool, error) {
	observedInstances := rc.GetObservedRunner()
	instance := rc.GetInstance()
	if observedInstances == nil || instance == nil || instance.Spec.InstanceSet.Replicas == nil {
		return false, nil
	}

	desired := int(*instance.Spec.InstanceSet.Replicas)
	namesToKeep := getNamesNeedToKeep(rc)
	ready := 0
	for _, item := range observedInstances.List {
		if item != nil && namesToKeep.Has(item.Name) && len(item.Pods) == 1 && util.IsPodReady(item.Pods[0]) {
			ready++
		}
	}
	if ready < desired {
		return false, nil
	}

	rollout := func(primary bool) (bool, error) {
		for _, item := range orderedObservedRunners(instance, observedInstances) {
			if item == nil || !namesToKeep.Has(item.Name) || len(item.Pods) != 1 || naming.IsMasterPod(item.Pods[0]) != primary {
				continue
			}
			matches, known := item.PodMatchesPodTemplate()
			if !known || matches {
				continue
			}
			pod := item.Pods[0]
			if primary && naming.IsPGEngine(instance) && desired > 1 {
				candidate := ""
				if instance.Status.PostgreSQL != nil {
					for _, member := range instance.Status.PostgreSQL.Members {
						if member.Role == "replica" && member.Ready && member.Running {
							candidate = member.Name
							break
						}
					}
				}
				if candidate == "" {
					return false, fmt.Errorf("cannot replace PostgreSQL primary without a ready switchover candidate")
				}
				operationID := "rolling-switchover-" + string(pod.UID)
				payload := map[string]interface{}{"requestId": operationID, "operationId": operationID, "leader": pod.Name, "candidate": candidate, "expectedTerm": instance.Status.PostgreSQL.Term, "reason": "replica-first-lifecycle-rollout"}
				if err := callPostgreSQLNativeAction(rc, pod.Name, "switchover", payload); err != nil {
					return true, err
				}
				return true, nil
			}
			uid := pod.GetUID()
			version := pod.GetResourceVersion()
			exactly := client.Preconditions{UID: &uid, ResourceVersion: &version}
			if err := rc.Client().Delete(rc.Context(), pod, exactly); err != nil {
				return true, client.IgnoreNotFound(err)
			}
			return true, nil
		}
		return false, nil
	}

	if rolling, err := rollout(false); rolling || err != nil {
		return rolling, err
	}
	return rollout(true)
}

func callPostgreSQLNativeAction(rc *context.InstanceContext, podName, action string, value map[string]interface{}) error {
	instance := rc.GetInstance()
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	httpClient, username, password, err := postgresqlruntime.Client(rc.Context(), rc.Client(), instance, 20*time.Second)
	if err != nil {
		return err
	}
	endpoint := postgresqlruntime.PodEndpoint(instance, podName) + "/v1/postgresql/actions/" + action
	req, err := http.NewRequestWithContext(rc.Context(), http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(username, password)
	response, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("PostgreSQL %s API returned HTTP %d", action, response.StatusCode)
	}
	return nil
}

// preparePostgreSQLCredentialRotation updates database role passwords before
// any replica starts with a new Secret. Existing sessions survive ALTER ROLE,
// so the following replica-first OnDelete rollout remains available.
func preparePostgreSQLCredentialRotation(rc *context.InstanceContext) error {
	observedInstances := rc.GetObservedRunner()
	instance := rc.GetInstance()
	if observedInstances == nil || instance == nil || instance.Spec.PostgreSQL == nil || instance.Status.PostgreSQL == nil || instance.Status.PostgreSQL.Primary == "" {
		return nil
	}
	rotationRequired := false
	for _, item := range observedInstances.List {
		if item == nil || item.Runner == nil || len(item.Pods) != 1 {
			continue
		}
		desired := item.Runner.Spec.Template.Annotations["postgresql.kdb.com/security-revision"]
		current := item.Pods[0].Annotations["postgresql.kdb.com/security-revision"]
		if desired != "" && desired != current {
			rotationRequired = true
		}
	}
	if !rotationRequired {
		return nil
	}

	secretMeta := naming.PostgreSQLCredentialSecret(instance)
	if instance.Spec.PostgreSQL.CredentialSecretRef != nil && instance.Spec.PostgreSQL.CredentialSecretRef.Name != "" {
		secretMeta.Name = instance.Spec.PostgreSQL.CredentialSecretRef.Name
	}
	secret := &corev1.Secret{}
	if err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: secretMeta.Name}, secret); err != nil {
		return err
	}
	credentials := map[string]string{
		"superuser":   string(secret.Data[naming.PostgreSQLSuperuserPasswordKey]),
		"replication": string(secret.Data[naming.PostgreSQLReplicationPasswordKey]),
		"backup":      string(secret.Data[naming.PostgreSQLBackupPasswordKey]),
		"monitoring":  string(secret.Data[naming.PostgreSQLMonitoringPasswordKey]),
	}
	requestID := "credential-rotation-" + secret.ResourceVersion
	payload, err := json.Marshal(map[string]interface{}{"requestId": requestID, "operationId": requestID, "reason": "operator-managed-secret-rotation", "credentials": credentials})
	if err != nil {
		return err
	}
	httpClient, username, password, err := postgresqlruntime.Client(rc.Context(), rc.Client(), instance, 15*time.Second)
	if err != nil {
		return err
	}
	endpoint := postgresqlruntime.PodEndpoint(instance, instance.Status.PostgreSQL.Primary) + "/v1/postgresql/actions/rotate-credentials"
	req, err := http.NewRequestWithContext(rc.Context(), http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(username, password)
	response, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("credential rotation API returned HTTP %d", response.StatusCode)
	}
	return nil
}

func getNamesNeedToKeep(rc *context.InstanceContext) sets.String {
	instance := rc.GetInstance()
	observedInstances := rc.GetObservedRunner()
	if instance == nil || observedInstances == nil || naming.InstanceSetSpec(instance).Replicas == nil {
		return sets.NewString()
	}
	// want defines the number of replicas we want for each instance set
	wantNums := *naming.InstanceSetSpec(instance).Replicas
	namesToKeep := sets.NewString()
	if instance.Spec.Shutdown != nil && *instance.Spec.Shutdown {
		return stoppedInstanceStatefulSetNames(instance)
	}
	if wantNums > 0 {
		for _, ins := range orderedObservedRunners(instance, observedInstances) {
			if ins == nil {
				continue
			}
			if len(ins.Pods) > 0 && naming.IsMasterPod(ins.Pods[0]) {
				namesToKeep.Insert(ins.Name)
			}
		}
	}
	// A synchronous replica is part of the current commit quorum. Preserve it
	// before choosing arbitrary replicas so a scale-down never removes the only
	// synchronous acknowledgement path while an async replica remains.
	if naming.IsPGEngine(instance) && instance.Status.PostgreSQL != nil {
		synchronousPods := sets.NewString()
		for _, member := range instance.Status.PostgreSQL.Members {
			if member.Role == "replica" && member.Synchronous {
				synchronousPods.Insert(member.Name)
			}
		}
		for _, ins := range orderedObservedRunners(instance, observedInstances) {
			if ins == nil {
				continue
			}
			if namesToKeep.Len() >= int(wantNums) {
				break
			}
			if len(ins.Pods) > 0 && synchronousPods.Has(ins.Pods[0].Name) {
				namesToKeep.Insert(ins.Name)
			}
		}
	}

	for _, ins := range orderedObservedRunners(instance, observedInstances) {
		if ins == nil {
			continue
		}
		if len(ins.Pods) > 0 && !naming.IsMasterPod(ins.Pods[0]) && namesToKeep.Len() < int(wantNums) {
			namesToKeep.Insert(ins.Name)
		}
	}
	for _, ins := range orderedObservedRunners(instance, observedInstances) {
		if ins == nil {
			continue
		}
		if namesToKeep.Len() >= int(wantNums) {
			break
		}
		namesToKeep.Insert(ins.Name)
	}
	return namesToKeep
}

// orderedObservedRunners makes member retention independent of informer list
// order. Keeping the lowest ordinals means scale-in removes the highest
// ordinal non-primary member, while the explicit primary preservation above
// prevents a failover-created primary from being selected as the victim.
func orderedObservedRunners(instance *v1.KDBInstance, observedInstances *observed.ObservedRunner) []*observed.SingleRunner {
	if observedInstances == nil {
		return nil
	}
	ordered := append([]*observed.SingleRunner(nil), observedInstances.List...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i] == nil {
			return false
		}
		if ordered[j] == nil {
			return true
		}
		left := naming.InstanceStsNum(instance, ordered[i].Name)
		right := naming.InstanceStsNum(instance, ordered[j].Name)
		if left != right {
			return left < right
		}
		return ordered[i].Name < ordered[j].Name
	})
	return ordered
}

func stoppedInstanceStatefulSetNames(instance *v1.KDBInstance) sets.String {
	names := sets.NewString()
	if instance == nil || instance.Spec.InstanceSet.Replicas == nil {
		return names
	}
	for i := 0; i < int(*instance.Spec.InstanceSet.Replicas); i++ {
		names.Insert(naming.InstanceStatefulSetName(instance.Name, i))
	}
	return names
}

// deleteSts will delete all resources related to a single sts
func deleteSts(rc *context.InstanceContext, stsName string) error {
	sts := &appsv1.StatefulSet{}
	key := client.ObjectKey{Namespace: rc.Namespace(), Name: stsName}
	if err := rc.Client().Get(rc.Context(), key, sts); err == nil {
		return errors.WithStack(client.IgnoreNotFound(rc.Client().Delete(rc.Context(), sts)))
	} else if !apierrors.IsNotFound(err) {
		return errors.WithStack(err)
	}
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := rc.Client().List(rc.Context(), pvcs, client.InNamespace(rc.Namespace()), client.MatchingLabels{naming.LabelInstanceSet: stsName}); err != nil {
		return errors.WithStack(err)
	}
	for i := range pvcs.Items {
		if err := rc.Client().Delete(rc.Context(), &pvcs.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return errors.WithStack(err)
		}
		return nil
	}
	return nil
}

func (s *InstanceStepManager) SetMonitor() kube.BindFunc {
	return s.StepBinder(
		"SetMonitor",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			if !mysqlMonitoringEnabled(instance) {
				return flow.Pass()
			}
			for _, obj := range mysqlMonitoringObjects(instance) {
				if err := errors.WithStack(ApplyMonitoringObject(rc, obj)); err != nil {
					if isMonitoringCRDMissing(err) {
						return flow.Pass()
					}
					return flow.Error(err, "apply monitor resource err")
				}
			}
			return flow.Pass()
		})
}

func mysqlMonitoringEnabled(instance *v1.KDBInstance) bool {
	return instance != nil &&
		naming.IsMySQLEngine(instance) &&
		instance.Spec.MySQL != nil &&
		instance.Spec.MySQL.Exporter != nil &&
		instance.Spec.MySQL.Exporter.Enabled
}

func mysqlMonitoringObjects(instance *v1.KDBInstance) []*unstructured.Unstructured {
	return []*unstructured.Unstructured{
		mysqlPodMonitor(instance),
		mysqlPrometheusRule(instance),
	}
}

func mysqlPodMonitor(instance *v1.KDBInstance) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PodMonitor",
		"metadata": map[string]interface{}{
			"name":      instance.Name + "-mysql",
			"namespace": instance.Namespace,
			"labels":    monitoringResourceLabels(instance, "podmonitor"),
		},
		"spec": map[string]interface{}{
			"namespaceSelector": map[string]interface{}{
				"matchNames": []interface{}{instance.Namespace},
			},
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					naming.LabelInstance: instance.Name,
				},
			},
			"podMetricsEndpoints": []interface{}{
				map[string]interface{}{
					"port":        naming.PortMySQLMetrics,
					"path":        "/metrics",
					"interval":    "30s",
					"relabelings": internalmonitoring.PodTargetRelabelings(instance),
				},
				map[string]interface{}{
					"port":        naming.PortSidecarMetrics,
					"path":        "/metrics",
					"scheme":      "https",
					"interval":    "30s",
					"relabelings": internalmonitoring.PodTargetRelabelings(instance),
					"tlsConfig": map[string]interface{}{
						"ca": map[string]interface{}{
							"secret": map[string]interface{}{
								"name": naming.MySQLCredentialSecret(instance).Name,
								"key":  "ca.crt",
							},
						},
						"cert": map[string]interface{}{
							"secret": map[string]interface{}{
								"name": naming.MySQLCredentialSecret(instance).Name,
								"key":  "client.crt",
							},
						},
						"keySecret": map[string]interface{}{
							"name": naming.MySQLCredentialSecret(instance).Name,
							"key":  "client.key",
						},
						"serverName":         "localhost",
						"insecureSkipVerify": false,
					},
				},
			},
		},
	}}
	obj.SetGroupVersionKind(schemaGroupVersionKind("monitoring.coreos.com/v1", "PodMonitor"))
	return obj
}

func mysqlPrometheusRule(instance *v1.KDBInstance) *unstructured.Unstructured {
	podMatcher := fmt.Sprintf(`namespace="%s",pod=~"%s.*"`, instance.Namespace, instance.Name)
	rules := internalmonitoring.InstanceResourceRecordingRules(instance)
	rules = append(rules,
		map[string]interface{}{
			"alert": "KDBMySQLExporterDown",
			"expr":  fmt.Sprintf(`mysql_up{%s} == 0`, podMatcher),
			"for":   "2m",
			"labels": map[string]interface{}{
				"severity": "critical",
				"engine":   "mysql",
			},
			"annotations": map[string]interface{}{
				"summary": "MySQL exporter is down",
			},
		},
		map[string]interface{}{
			"alert": "KDBMySQLSidecarDown",
			"expr":  fmt.Sprintf(`kdb_sidecar_up{%s} == 0`, podMatcher),
			"for":   "2m",
			"labels": map[string]interface{}{
				"severity": "critical",
				"engine":   "mysql",
			},
			"annotations": map[string]interface{}{
				"summary": "KDB MySQL sidecar metrics are down",
			},
		},
		map[string]interface{}{
			"alert": "KDBMySQLSQLProbeFailed",
			"expr":  fmt.Sprintf(`kdb_mysql_mgr_sql_up{%s} == 0`, podMatcher),
			"for":   "1m",
			"labels": map[string]interface{}{
				"severity": "critical",
				"engine":   "mysql",
			},
			"annotations": map[string]interface{}{
				"summary": "KDB MySQL SQL read probe failed",
			},
		},
		map[string]interface{}{
			"alert": "KDBMySQLSQLWriteProbeFailed",
			"expr":  fmt.Sprintf(`kdb_mysql_mgr_sql_write_up{%s} == 0`, podMatcher),
			"for":   "1m",
			"labels": map[string]interface{}{
				"severity": "critical",
				"engine":   "mysql",
			},
			"annotations": map[string]interface{}{
				"summary": "KDB MySQL SQL session write probe failed",
			},
		},
		map[string]interface{}{
			"alert": "KDBMySQLReplicationLagHigh",
			"expr":  fmt.Sprintf(`kdb_mysql_mgr_replication_lag_seconds{%s} > 60`, podMatcher),
			"for":   "5m",
			"labels": map[string]interface{}{
				"severity": "warning",
				"engine":   "mysql",
			},
			"annotations": map[string]interface{}{
				"summary": "KDB MySQL replication lag is high",
			},
		},
	)
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata": map[string]interface{}{
			"name":      instance.Name + "-mysql",
			"namespace": instance.Namespace,
			"labels":    monitoringResourceLabels(instance, "rules"),
		},
		"spec": map[string]interface{}{
			"groups": []interface{}{map[string]interface{}{
				"name":  "kdb.mysql." + instance.Name,
				"rules": rules,
			}},
		},
	}}
	obj.SetGroupVersionKind(schemaGroupVersionKind("monitoring.coreos.com/v1", "PrometheusRule"))
	return obj
}

func monitoringResourceLabels(instance *v1.KDBInstance, component string) map[string]interface{} {
	return map[string]interface{}{
		"app.kubernetes.io/name":       instance.Name,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": "kdb-operator",
		"kdb.io/monitoring":            "enabled",
		naming.LabelInstance:           instance.Name,
	}
}

func schemaGroupVersionKind(apiVersion, kind string) schema.GroupVersionKind {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionKind{Kind: kind}
	}
	return gv.WithKind(kind)
}

func isMonitoringCRDMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no matches for kind") ||
		strings.Contains(msg, "the server could not find the requested resource")
}
