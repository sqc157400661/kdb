package webhook

import (
	"context"
	"encoding/json"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestPostgreSQLLifecycleValidatorRejectsUnsafeMutations(t *testing.T) {
	three := int32(3)
	oldInstance := &v1.KDBInstance{}
	oldInstance.Spec.Engine = "postgresql"
	oldInstance.Spec.EngineVersion = "16"
	oldInstance.Spec.InstanceSet.Replicas = &three
	oldInstance.Spec.InstanceSet.DataVolumeClaimSpec.Size = resource.MustParse("10Gi")
	oldInstance.Spec.PostgreSQL = &v1.PostgreSQLSpec{HA: &v1.PostgreSQLHASpec{Profile: "standard"}}
	oldInstance.Status.PostgreSQL = &v1.PostgreSQLStatus{Primary: "postgres-0"}

	tests := []struct {
		name   string
		mutate func(*v1.KDBInstance)
	}{
		{name: "below HA minimum", mutate: func(value *v1.KDBInstance) { one := int32(1); value.Spec.InstanceSet.Replicas = &one }},
		{name: "PVC shrink", mutate: func(value *v1.KDBInstance) {
			value.Spec.InstanceSet.DataVolumeClaimSpec.Size = resource.MustParse("9Gi")
		}},
		{name: "unsigned custom extension image", mutate: func(value *v1.KDBInstance) {
			value.Spec.PostgreSQL.Lifecycle = &v1.PostgreSQLLifecycleSpec{Extensions: []v1.PostgreSQLExtensionSpec{{Name: "custom", CustomImage: &v1.PostgreSQLCustomExtensionImage{Image: "registry/custom:latest"}}}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldRaw, _ := json.Marshal(oldInstance)
			updated := oldInstance.DeepCopy()
			test.mutate(updated)
			newRaw, _ := json.Marshal(updated)
			response := (PostgreSQLLifecycleValidator{}).Handle(context.Background(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Object: runtimeRaw(newRaw), OldObject: runtimeRaw(oldRaw)}})
			if response.Allowed {
				t.Fatalf("expected unsafe mutation to be denied")
			}
		})
	}
}

func runtimeRaw(value []byte) runtime.RawExtension { return runtime.RawExtension{Raw: value} }
