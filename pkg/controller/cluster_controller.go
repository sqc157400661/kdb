package controller

import (
	"context"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/config"
	reconcile_context "github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// KDBClusterControllerName is the name of the KDBCluster controller
	KDBClusterControllerName = "mysql-instance-controller"
)

// KDBClusterReconciler holds resources for the KDBCluster reconciler
type KDBClusterReconciler struct {
	kube.ReconcileHelper
	Owner    client.FieldOwner
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=kdb.com,resources=KDBClusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=kdb.com,resources=KDBClusters/status,verbs=patch

// Reconcile reconciles a ConfigMap in a namespace managed by the PostgreSQL Operator
func (r *KDBClusterReconciler) Reconcile(
	ctx context.Context, request reconcile.Request) (reconcile.Result, error,
) {
	logger := log.FromContext(ctx).WithName("controllers").WithName("mysql-instance")
	task := kube.NewTask()
	rc := reconcile_context.NewClusterContext(kube.NewBaseReconcileContext(r, ctx, request, r.Owner, r.Recorder))
	// control the tuning tasks under the current namespace, generally used for emergency and grayscale processes
	kube.AbortWhen(config.IsNamespacePaused(request.Namespace), "Reconciling is paused, skip")(task)
	// get the mysql instance from the cache
	kdbCluster, err := rc.InitCluster()
	if err != nil {
		return reconcile.Result{}, err
	}
	if kdbCluster == nil || kdbCluster.Name == "" {
		return reconcile.Result{}, nil
	}

	// if the reconcile has been stopped,skip it
	kube.AbortWhen(rc.IsStopReconcile(), "instance is stop reconcile, skipped")(task)

	var stepManager steps.ClusterStepManager
	// Check for and handle deletion of cluster.
	kube.AbortWhen(rc.IsDeleted(), "instance is deleted, skipped")(task)
	kube.Branch(rc.IsDeleting(), stepManager.HandleDelete(), stepManager.CheckAndSetFinalizer())(task)
	stepManager.InitObservedInstance()(task)

	return kube.NewExecutor(logger).Execute(rc, task)
}

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=endpoints,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch

// SetupWithManager adds the KDBCluster controller to the provided runtime manager
func (r *KDBClusterReconciler) SetupWithManager(mgr manager.Manager) error {
	return builder.ControllerManagedBy(mgr).
		For(&v1.KDBCluster{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Endpoints{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&v1.KDBInstance{}).
		Complete(r)
}
