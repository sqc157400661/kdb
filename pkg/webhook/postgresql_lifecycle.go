package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/postgresqllifecycle"
	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const PostgreSQLLifecyclePath = "/validate-kdb-com-v1-kdbinstance-postgresql-lifecycle"

// +kubebuilder:webhook:path=/validate-kdb-com-v1-kdbinstance-postgresql-lifecycle,mutating=false,failurePolicy=fail,sideEffects=None,groups=kdb.com,resources=kdbinstances,verbs=create;update,versions=v1,name=vpostgresqllifecycle.kdb.com,admissionReviewVersions=v1

// PostgreSQLLifecycleValidator rejects unsafe PostgreSQL lifecycle mutations
// before they can reach the reconciler. The API service performs the same
// checks, while this webhook protects the internal CRD boundary.
type PostgreSQLLifecycleValidator struct{}

func (PostgreSQLLifecycleValidator) Handle(_ context.Context, request admission.Request) admission.Response {
	instance := &v1.KDBInstance{}
	if err := json.Unmarshal(request.Object.Raw, instance); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	var oldInstance *v1.KDBInstance
	if request.Operation == admissionv1.Update {
		oldInstance = &v1.KDBInstance{}
		if err := json.Unmarshal(request.OldObject.Raw, oldInstance); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	}
	if err := postgresqllifecycle.ValidateUpdate(oldInstance, instance); err != nil {
		return admission.Denied(err.Error())
	}
	return admission.Allowed("PostgreSQL lifecycle request is safe")
}
