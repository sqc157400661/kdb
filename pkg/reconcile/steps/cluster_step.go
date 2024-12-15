package steps

import (
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	"github.com/sqc157400661/kdb/internal/config"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

type Condition func(rc any, log logr.Logger) (bool, error)
type Step func(rc any, flow kube.Flow) (reconcile.Result, error)

type ClusterStepper interface {
	StepBinder(name string, f StepFunc) kube.BindFunc
	StepIfBinder(conditionName string, condFunc ConditionFunc, binders ...kube.BindFunc) kube.BindFunc
	CheckAndSetFinalizer() kube.BindFunc
	HandleDelete() kube.BindFunc
	SetGlobalConfig() kube.BindFunc
	SetInstanceConfig() kube.BindFunc
}

type ClusterStepManager struct {
}

// StepBinder bind one step to a task function
func (s *ClusterStepManager) StepBinder(name string, f Step) kube.BindFunc {
	return kube.NewStepBinder(
		kube.NewStep(
			name, func(rc kube.ReconcileContext, flow kube.Flow) (reconcile.Result, error) {
				return f(rc.(*context.ClusterContext), flow)
			},
		),
	)
}

// StepIfBinder bind one condition step to a task function
func (s *ClusterStepManager) StepIfBinder(conditionName string, condFunc Condition, binders ...kube.BindFunc) kube.BindFunc {
	condition := kube.NewCachedCondition(
		kube.NewCondition(conditionName, func(rc kube.ReconcileContext, log logr.Logger) (bool, error) {
			return condFunc(rc.(*context.ClusterContext), log)
		}),
	)

	ifBinders := make([]kube.BindFunc, len(binders))
	for i := range binders {
		ifBinders[i] = kube.NewStepIfBinder(condition, kube.ExtractStepsFromBindFunc(binders[i])[0])
	}

	return kube.CombineBinders(ifBinders...)
}

// CheckAndSetFinalizer check if the Finalizer exists, if not, add it
func (s *ClusterStepManager) CheckAndSetFinalizer() kube.BindFunc {
	//return s.StepBinder(
	//	"CheckAndSetFinalizer",
	//	func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
	//		if !rc.IsDeleted() && !rc.IsDeleting() {
	//			if rc.HasFinalizer(naming.Finalizer) {
	//				return flow.Pass()
	//			}
	//			// The cluster is not being deleted and needs a finalizer; set it.
	//
	//			// The Finalizers field is shared by multiple controllers, but the
	//			// server-side merge strategy does not work on our custom resource due
	//			// to a bug in Kubernetes. Build a merge-patch that includes the full
	//			// list of Finalizers plus ResourceVersion to detect conflicts with
	//			// other potential writers.
	//			// - https://issue.k8s.io/99730
	//			before := rc.GetInstance().DeepCopy()
	//			// Make another copy so that Patch doesn't write back to cluster.
	//			intent := before.DeepCopy()
	//			intent.Finalizers = append(intent.Finalizers, naming.Finalizer)
	//			err := errors.WithStack(rc.Patch(intent,
	//				client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})))
	//			if err != nil {
	//				return flow.Error(err, "patch finalizers error")
	//			}
	//		}
	//		return flow.Pass()
	//	})
}

// HandleDelete sets a finalizer on cluster and performs the finalization of
// cluster when it is being deleted. It returns (nil, nil) when cluster is
// not being deleted. The caller is responsible for returning other values to
// controller-runtime.
func (s *ClusterStepManager) HandleDelete() kube.BindFunc {
	return s.StepBinder(
		"HandleDelete",
		func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
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
func (s *ClusterStepManager) SetGlobalConfig() kube.BindFunc {
	return s.StepBinder(
		"SetGlobalConfig",
		func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
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

func (s *ClusterStepManager) SetInstanceConfig() kube.BindFunc {
	return s.StepBinder(
		"SetInstanceConfig",
		func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
			return flow.Pass()
		})
}
