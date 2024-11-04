package common

import (
	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var PatchKDBInstanceStatus = context.NewStepBinder(
	"PatchKDBInstanceStatus",
	func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
		err := rc.PatchKDBInstanceStatus()
		if err != nil {
			return flow.Error(err, "patch mysql instance Status err")
		}
		return flow.Pass()
	})

var PatchKDBInstance = context.NewStepBinder(
	"PatchKDBInstance",
	func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
		err := rc.PatchKDBInstance()
		if err != nil {
			return flow.Error(err, "patch mysql instance err")
		}
		return flow.Pass()
	})

var CheckAndSetFinalizer = context.NewStepBinder("CheckAndSetFinalizer", func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
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
