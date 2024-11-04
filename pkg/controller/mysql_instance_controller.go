package controller

import (
	"context"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/mysql.kdb.com/v1"
	reconcile_context "github.com/sqc157400661/kdb/pkg/reconcile/context"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/tools/record"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strconv"
)

const (
	// MySQLInstanceControllerName is the name of the MySQLInstance controller
	MySQLInstanceControllerName = "mysql-instance-controller"
)

// MySQLInstanceReconciler holds resources for the PostgresCluster reconciler
type MySQLInstanceReconciler struct {
	kube.ReconcileHelper
	Owner    client.FieldOwner
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=dbpaas.com,resources=postgresclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=dbpaas.com,resources=postgresclusters/status,verbs=patch

// Reconcile reconciles a ConfigMap in a namespace managed by the PostgreSQL Operator
func (r *MySQLInstanceReconciler) Reconcile(
	ctx context.Context, request reconcile.Request) (reconcile.Result, error,
) {
	logger := log.FromContext(ctx).WithName("controllers").WithName("postgresCluster")
	task := kube.NewTask()
	rc := reconcile_context.NewMySQLContext(kube.NewBaseReconcileContext(r, ctx, request, r.Owner, r.Recorder))
	return kube.NewExecutor(logger).Execute(rc, task)
}

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=endpoints,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch

// SetupWithManager adds the PostgresCluster controller to the provided runtime manager
func (r *MySQLInstanceReconciler) SetupWithManager(mgr manager.Manager) error {
	var opts controller.Options

	// TODO: Move this to main with controller-runtime v0.9+
	// - https://github.com/kubernetes-sigs/controller-runtime/commit/82fc2564cf
	if s := os.Getenv("KDB_MySQL_WORKERS"); s != "" {
		if i, err := strconv.Atoi(s); err == nil && i > 0 {
			opts.MaxConcurrentReconciles = i
		} else {
			mgr.GetLogger().Error(err, "KDB_MySQL_WORKERS must be a positive number")
		}
	}
	if opts.MaxConcurrentReconciles == 0 {
		opts.MaxConcurrentReconciles = 2
	}

	return builder.ControllerManagedBy(mgr).
		For(&v1.MySQLInstance{}).
		WithOptions(opts).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Endpoints{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&batchv1.Job{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Complete(r)
}
