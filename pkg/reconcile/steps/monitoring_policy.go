package steps

import (
	internalmonitoring "github.com/sqc157400661/kdb/internal/monitoring"
	reconcilecontext "github.com/sqc157400661/kdb/pkg/reconcile/context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ApplyMonitoringObject preserves the existing PodMonitor path and inserts the
// alert-policy bundle state machine only for PrometheusRule resources. The
// Operator remains the sole writer of the effective rule and status projection.
func ApplyMonitoringObject(rc *reconcilecontext.InstanceContext, object *unstructured.Unstructured) error {
	if rc == nil || object == nil {
		return internalmonitoring.ErrAlertPolicyBundleInvalid
	}
	decision := internalmonitoring.AlertPolicyDecision{Rule: object, Apply: true}
	if object.GetKind() == "PrometheusRule" {
		resolved, err := internalmonitoring.ResolveAlertPolicyForInstance(rc.Context(), rc.Client(), rc.GetInstance(), object)
		if err != nil {
			return err
		}
		decision = resolved
		if !decision.Apply {
			return nil
		}
		object = decision.Rule
	}
	if err := rc.SetOwnerReference(object); err != nil {
		return err
	}
	if err := rc.Apply(object); err != nil {
		return err
	}
	if object.GetKind() == "PrometheusRule" {
		return internalmonitoring.MarkAlertPolicyBundleApplied(rc.Context(), rc.Client(), rc.GetInstance(), decision)
	}
	return nil
}
