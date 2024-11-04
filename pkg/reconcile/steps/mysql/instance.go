package mysql

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

// HandleDelete sets a finalizer on cluster and performs the finalization of
// cluster when it is being deleted. It returns (nil, nil) when cluster is
// not being deleted. The caller is responsible for returning other values to
// controller-runtime.
var HandleDelete = context.NewStepBinder("HandleDelete", func(rc *db_context.Context, flow control.Flow) (reconcile.Result, error) {
	// Find all instance pods to determine which to shutdown and in what order.
	pods := &corev1.PodList{}
	instances, err := naming.AsSelector(naming.ClusterInstances(rc.Name()))
	if err == nil {
		err = errors.WithStack(rc.List(pods, instances))
	}
	if err != nil {
		return flow.Error(err, "get pod list err")
	}

	if len(pods.Items) == 0 {
		// Instances are stopped, now cleanup some Patroni stuff.
		// as Patroni creates them. Would their events cause too many reconciles?
		// Foreground deletion may force us to adopt and set finalizers anyway.
		var selector labels.Selector
		selector, err = naming.AsSelector(naming.ClusterPatronis(rc.PostgresCluster()))
		if err == nil {
			err = errors.WithStack(
				rc.Client().DeleteAllOf(rc.Context(), &corev1.Endpoints{},
					client.InNamespace(rc.Namespace()),
					client.MatchingLabelsSelector{Selector: selector},
				))
		}
		if err != nil {
			return flow.Error(err, "delete patroni stuff err")
		}
		// Our finalizer logic is finished; remove our finalizer.
		// The Finalizers field is shared by multiple controllers, but the
		// server-side merge strategy does not work on our custom resource due to a
		// bug in Kubernetes. Build a merge-patch that includes the full list of
		// Finalizers plus ResourceVersion to detect conflicts with other potential
		// writers.
		// - https://issue.k8s.io/99730
		before := rc.PostgresCluster().DeepCopy()
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

		if val, has := pod.Annotations[naming.DeleteConfirmKey]; has && val == "false" {
			patch := client.RawPatch(client.Merge.Type(), []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"true"}}}`, naming.DeleteConfirmKey)))
			return errors.WithStack(rc.Patch(pod, patch))
		}

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
		if role == naming.RolePatroniReplica || len(role) == 0 {
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
