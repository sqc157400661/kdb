package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
)

func TestMySQLRestoreCredentialRequestsOnlyEnqueueMatchingAdoptSourceTargets(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("add KDB scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	adopt := v1.MySQLRestoreCredentialModeAdoptSource
	otherMode := v1.MySQLRestoreCredentialModeTarget
	target := func(name string, mode v1.MySQLRestoreCredentialMode, source string) *v1.KDBInstance {
		return &v1.KDBInstance{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kdb"},
			Spec: v1.KDBInstanceSpec{MySQL: &v1.MySQLSpec{Restore: &v1.MySQLRestoreSpec{
				CredentialMode:    mode,
				SourceInstanceRef: &corev1.LocalObjectReference{Name: source},
			}}},
		}
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		target("restored", adopt, "source"),
		target("target-mode", otherMode, "source"),
		target("wrong-source", adopt, "another-source"),
		&v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "other"}},
	).Build()
	// The cross-namespace object is in a different namespace and must not be
	// selected by a namespace-local SourceInstanceRef contract.
	fakeClient := client
	if err := fakeClient.Create(context.Background(), &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "cross-namespace", Namespace: "other"}, Spec: v1.KDBInstanceSpec{MySQL: &v1.MySQLSpec{Restore: &v1.MySQLRestoreSpec{
		CredentialMode:    adopt,
		SourceInstanceRef: &corev1.LocalObjectReference{Name: "source"},
	}}}}); err != nil {
		t.Fatalf("create cross-namespace object: %v", err)
	}
	handler := mysqlRestoreCredentialRequests(client)
	requests := handler(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "source-mysql-credential", Namespace: "kdb"}})
	if len(requests) != 1 || requests[0].NamespacedName != (types.NamespacedName{Namespace: "kdb", Name: "restored"}) {
		t.Fatalf("requests = %#v, want only kdb/restored", requests)
	}
	if got := handler(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "kdb"}}); got != nil {
		t.Fatalf("unrelated secret requests = %#v, want nil", got)
	}
}
