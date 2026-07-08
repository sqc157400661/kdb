package steps

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/observed"
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
	SetInstanceConfig() kube.BindFunc
	SetRbac() kube.BindFunc
	InitObservedRunner() kube.BindFunc
	SetService() kube.BindFunc
	ScaleUpInstance() kube.BindFunc
	ScaleDownInstance() kube.BindFunc
	SetMonitor() kube.BindFunc
	ReconcileProxySQL() kube.BindFunc
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

			// stop schedules pod for deletion by scaling its controller to zero.
			stop := func(pod *corev1.Pod) error {
				instance := &unstructured.Unstructured{}
				instance.SetNamespace(rc.Namespace())

				switch owner := metav1.GetControllerOfNoCopy(pod); {
				case owner == nil:
					return errors.Errorf("pod %q has no owner", client.ObjectKeyFromObject(pod))

				case owner.Kind == "StatefulSet":
					instance.SetAPIVersion(owner.APIVersion)
					instance.SetKind(owner.Kind)
					instance.SetName(owner.Name)

				default:
					return errors.Errorf("unexpected kind %q", owner.Kind)
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
			for i := 0; i < int(*instance.Spec.InstanceSet.Replicas); i++ {
				next := naming.GenerateInstanceStatefulSetMeta(instance, i)
				runners = append(runners, &appsv1.StatefulSet{ObjectMeta: next})
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
				rolling, err := rolloutOutdatedMySQLPod(rc)
				if err != nil {
					return flow.Error(err, "roll out outdated mysql pod err")
				}
				if rolling {
					return flow.RetryAfter(5*time.Second, "mysql pod rollout in progress")
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

// rolloutOutdatedMySQLPod advances an OnDelete StatefulSet update one ready Pod
// at a time. Replica Pods are replaced before the primary so enabling an
// exporter or changing another Pod-template field does not restart every MySQL
// member at once.
func rolloutOutdatedMySQLPod(rc *context.InstanceContext) (bool, error) {
	observedInstances := rc.GetObservedRunner()
	instance := rc.GetInstance()
	if observedInstances == nil || instance == nil || instance.Spec.InstanceSet.Replicas == nil {
		return false, nil
	}

	desired := int(*instance.Spec.InstanceSet.Replicas)
	ready := 0
	for _, item := range observedInstances.List {
		if item != nil && len(item.Pods) == 1 && util.IsPodReady(item.Pods[0]) {
			ready++
		}
	}
	if ready < desired {
		return false, nil
	}

	rollout := func(primary bool) (bool, error) {
		for _, item := range observedInstances.List {
			if item == nil || len(item.Pods) != 1 || naming.IsMasterPod(item.Pods[0]) != primary {
				continue
			}
			matches, known := item.PodMatchesPodTemplate()
			if !known || matches {
				continue
			}
			pod := item.Pods[0]
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

func getNamesNeedToKeep(rc *context.InstanceContext) sets.String {
	instance := rc.GetInstance()
	observedInstances := rc.GetObservedRunner()
	// want defines the number of replicas we want for each instance set
	wantNums := *naming.InstanceSetSpec(instance).Replicas
	namesToKeep := sets.NewString()
	if instance.Spec.Shutdown != nil && *instance.Spec.Shutdown {
		return stoppedInstanceStatefulSetNames(instance)
	}
	if wantNums > 0 {
		for _, ins := range observedInstances.List {
			if len(ins.Pods) > 0 && naming.IsMasterPod(ins.Pods[0]) {
				namesToKeep.Insert(ins.Name)
			}
		}
	}

	for _, ins := range observedInstances.List {
		if len(ins.Pods) > 0 && !naming.IsMasterPod(ins.Pods[0]) && namesToKeep.Len() < int(wantNums) {
			namesToKeep.Insert(ins.Name)
		}
	}
	for _, ins := range observedInstances.List {
		if namesToKeep.Len() >= int(wantNums) {
			break
		}
		namesToKeep.Insert(ins.Name)
	}
	return namesToKeep
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
	sts := appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: stsName, Namespace: rc.Namespace()}}
	err := rc.Get(&sts)
	if client.IgnoreNotFound(err) != nil {
		return errors.WithStack(err)
	}
	err = errors.WithStack(client.IgnoreNotFound(rc.DeleteControlled(&sts)))
	if client.IgnoreNotFound(err) != nil {
		return err
	}
	for _, vol := range rc.Volumes() {
		if len(vol.Labels) > 0 && vol.Labels[naming.LabelInstanceSet] == stsName {
			err = errors.WithStack(client.IgnoreNotFound(rc.DeleteControlled(&vol)))
			if err == nil {
				return client.IgnoreNotFound(err)
			}
		}
	}
	return err
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
				if err := errors.WithStack(rc.SetOwnerReference(obj)); err != nil {
					return flow.Error(err, "set monitor owner ref err")
				}
				if err := errors.WithStack(rc.Apply(obj)); err != nil {
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
					"port":     naming.PortMySQLMetrics,
					"path":     "/metrics",
					"interval": "30s",
				},
				map[string]interface{}{
					"port":     naming.PortSidecarMetrics,
					"path":     "/metrics",
					"interval": "30s",
				},
			},
		},
	}}
	obj.SetGroupVersionKind(schemaGroupVersionKind("monitoring.coreos.com/v1", "PodMonitor"))
	return obj
}

func mysqlPrometheusRule(instance *v1.KDBInstance) *unstructured.Unstructured {
	podMatcher := fmt.Sprintf(`namespace="%s",pod=~"%s.*"`, instance.Namespace, instance.Name)
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
				"name": "kdb.mysql." + instance.Name,
				"rules": []interface{}{
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
				},
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
