package context

import (
	stdctx "context"
	"testing"

	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestPatchKDBInstanceStatusIgnoresNotFound(t *testing.T) {
	rc := newTestInstanceContext(t, "mysql-sqc-766000")
	old := testKDBInstance("mysql-sqc-766000", "kdb")
	instance := old.DeepCopy()
	instance.Status.Message = "creating"
	rc.oldInstance = old
	rc.instance = instance

	if err := rc.PatchKDBInstanceStatus(); err != nil {
		t.Fatalf("PatchKDBInstanceStatus() should ignore not found: %v", err)
	}
}

func TestPatchKDBInstanceIgnoresNotFound(t *testing.T) {
	rc := newTestInstanceContext(t, "mysql-sqc-766000")
	old := testKDBInstance("mysql-sqc-766000", "kdb")
	instance := old.DeepCopy()
	replicas := int32(2)
	instance.Spec.InstanceSet.Replicas = &replicas
	rc.oldInstance = old
	rc.instance = instance

	if err := rc.PatchKDBInstance(); err != nil {
		t.Fatalf("PatchKDBInstance() should ignore not found: %v", err)
	}
}

func newTestInstanceContext(t *testing.T, name string) *InstanceContext {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("add kdb scheme: %v", err)
	}
	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()
	helper := kube.NewDefaultReconcileHelper(kubeClient, nil, nil, scheme)
	base := kube.NewBaseReconcileContext(
		helper,
		stdctx.Background(),
		reconcile.Request{NamespacedName: client.ObjectKey{Name: name, Namespace: "kdb"}},
		client.FieldOwner("kdb-test"),
		record.NewFakeRecorder(1),
	)
	return NewInstanceContext(base)
}

func testKDBInstance(name, namespace string) *v1.KDBInstance {
	replicas := int32(1)
	return &v1.KDBInstance{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, ResourceVersion: "1"},
		Spec: v1.KDBInstanceSpec{
			InstanceSet:   shared.InstanceSetSpec{Replicas: &replicas},
			Engine:        "mysql",
			EngineVersion: "8.0",
		},
	}
}
