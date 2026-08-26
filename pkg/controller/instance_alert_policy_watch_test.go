package controller

import (
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	internalmonitoring "github.com/sqc157400661/kdb/internal/monitoring"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAlertPolicyBundleRequestsTargetsOnlyMatchingInstance(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	wanted := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "database", Labels: map[string]string{naming.LabelScopeInstance: "instance-a"}}}
	unrelated := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "billing", Namespace: "database", Labels: map[string]string{naming.LabelScopeInstance: "instance-b"}}}
	handler := alertPolicyBundleRequests(fake.NewClientBuilder().WithScheme(scheme).WithObjects(wanted, unrelated).Build())
	configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: internalmonitoring.AlertPolicyConfigMapName("instance-a"), Namespace: "database",
		Labels: map[string]string{internalmonitoring.AlertPolicyManagedLabel: "true", internalmonitoring.AlertPolicyInstanceLabel: "instance-a"},
	}}
	requests := handler(configMap)
	if len(requests) != 1 || requests[0].Namespace != "database" || requests[0].Name != "orders" {
		t.Fatalf("requests = %#v", requests)
	}

	configMap.Name = "forged-name"
	if requests := handler(configMap); len(requests) != 0 {
		t.Fatalf("forged bundle requests = %#v", requests)
	}
	configMap.Name = internalmonitoring.AlertPolicyConfigMapName("instance-a")
	configMap.Labels[internalmonitoring.AlertPolicyManagedLabel] = "false"
	if requests := handler(configMap); len(requests) != 0 {
		t.Fatalf("unmanaged bundle requests = %#v", requests)
	}
}
