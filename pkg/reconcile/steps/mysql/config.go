package mysql

import (
	"github.com/sqc157400661/helper/kube"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
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
