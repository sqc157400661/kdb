package steps

import (
	stdctx "context"
	"testing"

	"github.com/sqc157400661/helper/kube"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/observed"
	reconcilecontext "github.com/sqc157400661/kdb/pkg/reconcile/context"
)

func TestGetNamesNeedToKeepRemovesHighestOrdinalNonPrimary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		order      []string
		primary    string
		wantKeep   []string
		wantRemove string
	}{
		{name: "primary-low", order: []string{"mysql2", "mysql0", "mysql1"}, primary: "mysql0", wantKeep: []string{"mysql0", "mysql1"}, wantRemove: "mysql2"},
		{name: "primary-high", order: []string{"mysql1", "mysql2", "mysql0"}, primary: "mysql2", wantKeep: []string{"mysql0", "mysql2"}, wantRemove: "mysql1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instance := mysqlScaleDownInstance(2)
			runner := mysqlObservedRunner(tc.order, tc.primary, false)
			rc := mysqlScaleDownContext(t, instance, runner)

			got := getNamesNeedToKeep(rc)
			for _, name := range tc.wantKeep {
				if !got.Has(name) {
					t.Fatalf("kept names = %v, missing %s", got.List(), name)
				}
			}
			if got.Has(tc.wantRemove) {
				t.Fatalf("kept names = %v, unexpectedly retained highest removable ordinal %s", got.List(), tc.wantRemove)
			}
		})
	}
}

func TestRolloutOutdatedPodSkipsScaleInVictim(t *testing.T) {
	instance := mysqlScaleDownInstance(2)
	runner := mysqlObservedRunner([]string{"mysql2", "mysql1", "mysql0"}, "mysql0", true)
	objects := []client.Object{instance}
	for _, member := range runner.List {
		objects = append(objects, member.Runner, member.Pods[0])
	}
	rc := mysqlScaleDownContextWithObjects(t, instance, runner, objects...)

	rolling, err := rolloutOutdatedPod(rc)
	if err != nil {
		t.Fatalf("rolloutOutdatedPod() error = %v", err)
	}
	if !rolling {
		t.Fatal("rolloutOutdatedPod() rolling = false, want true")
	}
	if err := rc.Client().Get(stdctx.Background(), client.ObjectKey{Namespace: "kdb", Name: "mysql1-0"}, &corev1.Pod{}); err == nil {
		t.Fatal("kept outdated replica mysql1-0 was not deleted for rollout")
	}
	if err := rc.Client().Get(stdctx.Background(), client.ObjectKey{Namespace: "kdb", Name: "mysql2-0"}, &corev1.Pod{}); err != nil {
		t.Fatalf("scale-in victim mysql2-0 must not be rolled before StatefulSet deletion: %v", err)
	}
}

func mysqlScaleDownInstance(replicas int32) *v1.KDBInstance {
	return &v1.KDBInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "mysql", Namespace: "kdb"},
		Spec: v1.KDBInstanceSpec{
			Engine:      naming.MySQLEngine,
			DeployArch:  naming.MySQLMasterReplicaDeployArch,
			InstanceSet: shared.InstanceSetSpec{Replicas: &replicas},
		},
	}
}

func mysqlObservedRunner(order []string, primary string, outdated bool) *observed.ObservedRunner {
	runner := &observed.ObservedRunner{BySet: map[string]*observed.SingleRunner{}}
	for _, name := range order {
		role := naming.ReplicaRole
		if name == primary {
			role = naming.MasterRole
		}
		podRevision := "rev-current"
		if outdated && name != primary {
			podRevision = "rev-old"
		}
		member := &observed.SingleRunner{
			Name: name,
			Runner: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kdb", Generation: 1},
				Status:     appsv1.StatefulSetStatus{ObservedGeneration: 1, UpdateRevision: "rev-current"},
			},
			Pods: []*corev1.Pod{{
				ObjectMeta: metav1.ObjectMeta{
					Name: name + "-0", Namespace: "kdb", UID: types.UID("uid-" + name), ResourceVersion: "1",
					Labels: map[string]string{naming.LabelInstanceSet: name, naming.LabelRole: role, appsv1.StatefulSetRevisionLabel: podRevision},
				},
				Status: corev1.PodStatus{
					Phase:             corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{{Name: "mysql", Ready: true}},
				},
			}},
		}
		runner.List = append(runner.List, member)
		runner.BySet[name] = member
	}
	return runner
}

func mysqlScaleDownContext(t *testing.T, instance *v1.KDBInstance, runner *observed.ObservedRunner) *reconcilecontext.InstanceContext {
	return mysqlScaleDownContextWithObjects(t, instance, runner, instance)
}

func mysqlScaleDownContextWithObjects(t *testing.T, instance *v1.KDBInstance, runner *observed.ObservedRunner, objects ...client.Object) *reconcilecontext.InstanceContext {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	helper := kube.NewDefaultReconcileHelper(kubeClient, nil, nil, scheme)
	base := kube.NewBaseReconcileContext(helper, stdctx.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: instance.Name, Namespace: instance.Namespace},
	}, client.FieldOwner("kdb-test"), record.NewFakeRecorder(1))
	rc := reconcilecontext.NewInstanceContext(base)
	if _, err := rc.InitInstance(); err != nil {
		t.Fatal(err)
	}
	rc.SetObservedRunner(runner)
	return rc
}
