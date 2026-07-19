package pg

import (
	stdctx "context"
	"testing"
	"time"

	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	reconcilecontext "github.com/sqc157400661/kdb/pkg/reconcile/context"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestExternalFenceDeletesExpiredHolderThenConfirmsTerm(t *testing.T) {
	instance := postgreSQLFenceInstance()
	lease := newPostgreSQLLeaderLease(naming.PostgreSQLLeaderLeaseName(instance.Name), instance.Namespace, "demo-pg-0/uid-old", "uid-old", 7, 10, time.Now().Add(-time.Minute))
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "demo-pg-0", Namespace: instance.Namespace, UID: types.UID("uid-old"),
		Labels: map[string]string{naming.LabelInstance: instance.Name},
	}}
	rc := newFenceTestContext(t, instance, lease, pod)

	progressed, err := reconcilePostgreSQLExternalFence(rc, instance)
	if err != nil || !progressed {
		t.Fatalf("delete expired holder: progressed=%v err=%v", progressed, err)
	}
	if err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(pod), &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected holder Pod deletion, err=%v", err)
	}

	progressed, err = reconcilePostgreSQLExternalFence(rc, instance)
	if err != nil || !progressed {
		t.Fatalf("confirm fenced term: progressed=%v err=%v", progressed, err)
	}
	got := &coordinationv1.Lease{}
	if err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(lease), got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[postgresqlFencedTermAnnotation] != "7" {
		t.Fatalf("expected fenced term 7, got annotations=%v", got.Annotations)
	}
}

func TestExternalFenceDoesNotDeleteReplacementPod(t *testing.T) {
	instance := postgreSQLFenceInstance()
	lease := newPostgreSQLLeaderLease(naming.PostgreSQLLeaderLeaseName(instance.Name), instance.Namespace, "demo-pg-0/uid-old", "uid-old", 2, 10, time.Now().Add(-time.Minute))
	replacement := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "demo-pg-0", Namespace: instance.Namespace, UID: types.UID("uid-new"),
		Labels: map[string]string{naming.LabelInstance: instance.Name},
	}}
	rc := newFenceTestContext(t, instance, lease, replacement)

	progressed, err := reconcilePostgreSQLExternalFence(rc, instance)
	if err != nil || !progressed {
		t.Fatalf("confirm replaced holder: progressed=%v err=%v", progressed, err)
	}
	if err := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(replacement), &corev1.Pod{}); err != nil {
		t.Fatalf("replacement Pod must remain: %v", err)
	}
}

func TestExternalFenceIgnoresUnexpiredLease(t *testing.T) {
	instance := postgreSQLFenceInstance()
	lease := newPostgreSQLLeaderLease(naming.PostgreSQLLeaderLeaseName(instance.Name), instance.Namespace, "demo-pg-0/uid-old", "uid-old", 1, 60, time.Now())
	rc := newFenceTestContext(t, instance, lease)
	progressed, err := reconcilePostgreSQLExternalFence(rc, instance)
	if err != nil || progressed {
		t.Fatalf("unexpired Lease must be ignored: progressed=%v err=%v", progressed, err)
	}
}

func postgreSQLFenceInstance() *v1.KDBInstance {
	return &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "demo-pg", Namespace: "kdb"}}
}

func newFenceTestContext(t *testing.T, instance *v1.KDBInstance, objects ...client.Object) *reconcilecontext.InstanceContext {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1.AddToScheme, corev1.AddToScheme, coordinationv1.AddToScheme} {
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
