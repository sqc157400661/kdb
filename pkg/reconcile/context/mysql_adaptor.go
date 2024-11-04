package reconcile_context

import (
	"github.com/go-logr/logr"
	"github.com/sqc157400661/helper/kube"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ConditionFunc func(rc *MySQLContext, log logr.Logger) (bool, error)
type StepFunc func(rc *MySQLContext, flow kube.Flow) (reconcile.Result, error)

func NewMySQLStepBinder(name string, f StepFunc) kube.BindFunc {
	return kube.NewStepBinder(
		kube.NewStep(
			name, func(rc kube.ReconcileContext, flow kube.Flow) (reconcile.Result, error) {
				return f(rc.(*MySQLContext), flow)
			},
		),
	)
}

func NewMySQLStepIfBinder(conditionName string, condFunc ConditionFunc, binders ...kube.BindFunc) kube.BindFunc {
	condition := kube.NewCachedCondition(
		kube.NewCondition(conditionName, func(rc kube.ReconcileContext, log logr.Logger) (bool, error) {
			return condFunc(rc.(*MySQLContext), log)
		}),
	)

	ifBinders := make([]kube.BindFunc, len(binders))
	for i := range binders {
		ifBinders[i] = kube.NewStepIfBinder(condition, kube.ExtractStepsFromBindFunc(binders[i])[0])
	}

	return kube.CombineBinders(ifBinders...)
}
