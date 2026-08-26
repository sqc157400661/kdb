package naming

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

func TestKubernetesLabelValueMatchesAdminContract(t *testing.T) {
	const projected = "v-rred7nenr2gjkrfwjbwxo5uqfnvoos7f27ry2bdgyzvv7giqkqnq"
	if got := KubernetesLabelValue("volcengine/cn-beijing"); got != projected {
		t.Fatalf("projected region = %q, want %q", got, projected)
	}
	if problems := validation.IsValidLabelValue(projected); len(problems) != 0 {
		t.Fatalf("projected value is not a Kubernetes label: %v", problems)
	}
	if got := KubernetesLabelValue("prod"); got != "prod" {
		t.Fatalf("valid value = %q, want prod", got)
	}
}

func TestPlatformScopeMetadataUsesClosedKeys(t *testing.T) {
	source := map[string]string{
		LabelScopeTenant:      "default",
		LabelScopeProject:     "trade",
		LabelScopeEnvironment: "prod",
		LabelScopeRegion:      KubernetesLabelValue("volcengine/cn-beijing"),
		LabelScopeInstance:    "orders",
		AnnotationRawRegion:   "not-a-label",
		"caller.example/key":  "untrusted",
	}
	if got := PlatformScopeLabels(source); !reflect.DeepEqual(got, map[string]string{
		LabelScopeTenant:      "default",
		LabelScopeProject:     "trade",
		LabelScopeEnvironment: "prod",
		LabelScopeRegion:      KubernetesLabelValue("volcengine/cn-beijing"),
		LabelScopeInstance:    "orders",
	}) {
		t.Fatalf("scope labels = %#v", got)
	}
	annotations := map[string]string{
		AnnotationRawRegion:     "volcengine/cn-beijing",
		AnnotationScopeRevision: "17",
		"caller.example/key":    "untrusted",
	}
	if got := PlatformScopeAnnotations(annotations); !reflect.DeepEqual(got, map[string]string{
		AnnotationRawRegion:     "volcengine/cn-beijing",
		AnnotationScopeRevision: "17",
	}) {
		t.Fatalf("scope annotations = %#v", got)
	}
}
