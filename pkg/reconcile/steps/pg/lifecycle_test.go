package pg

import (
	stdctx "context"
	"testing"

	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	reconcilecontext "github.com/sqc157400661/kdb/pkg/reconcile/context"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestRemoveStaleMajorUpgradeJobsKeepsCurrentOperation(t *testing.T) {
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"}}
	labels := map[string]string{
		naming.LabelInstance:           instance.Name,
		"postgresql.kdb.com/lifecycle": "major-upgrade",
	}
	current := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "orders-major-current", Namespace: instance.Namespace, Labels: labels}}
	stale := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "orders-major-stale", Namespace: instance.Namespace, Labels: labels}}
	unrelated := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "orders-backup", Namespace: instance.Namespace, Labels: map[string]string{naming.LabelInstance: instance.Name}}}
	rc := newLifecycleTestContext(t, instance, current, stale, unrelated)

	removed, err := removeStaleMajorUpgradeJobs(rc, instance, current.Name)
	if err != nil || !removed {
		t.Fatalf("remove stale Jobs: removed=%v err=%v", removed, err)
	}
	for _, name := range []string{current.Name, unrelated.Name} {
		if err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: name}, &batchv1.Job{}); err != nil {
			t.Fatalf("Job %s must remain: %v", name, err)
		}
	}
	if err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(stale), &batchv1.Job{}); err == nil {
		t.Fatal("stale major-upgrade Job was not deleted")
	}
}

func TestPersistPostgreSQLUpgradeChangesOnlyMainImageForSplitRuntime(t *testing.T) {
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"}, Spec: v1.KDBInstanceSpec{
		EngineVersion: "17",
		InstanceSet: shared.InstanceSetSpec{MainContainer: shared.ContainerSpec{Image: "postgresql17:old"}, SidecarContainer: shared.ContainerSpec{Image: "postgresql-sidecar:stable"}},
	}}
	rc := newLifecycleTestContext(t, instance)
	changed, err := persistPostgreSQLUpgradeTarget(rc, instance, &v1.PostgreSQLUpgradeSpec{TargetImage: "postgresql17:new"}, false)
	if err != nil || !changed {
		t.Fatalf("persist split runtime upgrade: changed=%v err=%v", changed, err)
	}
	latest := &v1.KDBInstance{}
	if err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(instance), latest); err != nil {
		t.Fatal(err)
	}
	if latest.Spec.InstanceSet.MainContainer.Image != "postgresql17:new" || latest.Spec.InstanceSet.SidecarContainer.Image != "postgresql-sidecar:stable" {
		t.Fatalf("upgrade changed the wrong image: %#v", latest.Spec.InstanceSet)
	}
}

func newLifecycleTestContext(t *testing.T, instance *v1.KDBInstance, objects ...client.Object) *reconcilecontext.InstanceContext {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1.AddToScheme, batchv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	all := append([]client.Object{instance}, objects...)
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).Build()
	helper := kube.NewDefaultReconcileHelper(kubeClient, nil, nil, scheme)
	base := kube.NewBaseReconcileContext(helper, stdctx.Background(), reconcile.Request{
		NamespacedName: client.ObjectKeyFromObject(instance),
	}, client.FieldOwner("kdb-test"), record.NewFakeRecorder(1))
	rc := reconcilecontext.NewInstanceContext(base)
	if _, err := rc.InitInstance(); err != nil {
		t.Fatal(err)
	}
	return rc
}
