package controller

import (
	"context"
	"fmt"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/config"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/topology"
	reconcile_context "github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps/clickhouse"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps/mysql"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps/pg"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/tools/record"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"strconv"
	"strings"
	"time"
)

const (
	// KDBInstanceControllerName is the name of the KDBInstance controller
	KDBInstanceControllerName = "mysql-instance-controller"
)

// KDBInstanceReconciler holds resources for the KDBInstance reconciler
type KDBInstanceReconciler struct {
	kube.ReconcileHelper
	Owner    client.FieldOwner
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbinstances,verbs=get;list;patch;watch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbinstances/status,verbs=patch

// TODO:
// 1.实例创建后，没有标记role IsMasterPod判断有问题
// operator 可以使用demo进行部署，但是好像sidecar有点问题，无法构建主从

// Reconcile reconciles a ConfigMap in a namespace managed by the PostgreSQL Operator
func (r *KDBInstanceReconciler) Reconcile(
	ctx context.Context, request reconcile.Request) (reconcile.Result, error,
) {
	logger := log.FromContext(ctx).WithName("controllers").WithName("mysql-instance")
	task := kube.NewTask()

	rc := reconcile_context.NewInstanceContext(kube.NewBaseReconcileContext(r, ctx, request, r.Owner, r.Recorder))
	// control the tuning tasks under the current namespace, generally used for emergency and grayscale processes
	kube.AbortWhen(config.IsNamespacePaused(request.Namespace), "Reconciling is paused, skip")(task)

	// get the mysql instance from the cache
	kdbInstance, err := rc.InitInstance()
	if err != nil {
		return reconcile.Result{}, err
	}
	if kdbInstance == nil || kdbInstance.Name == "" {
		return reconcile.Result{}, nil
	}
	if err = topology.ValidateInstanceSpec(kdbInstance); err != nil {
		return reconcile.Result{}, err
	}
	if err = topology.ValidateClickHouseSpec(kdbInstance, nil); err != nil {
		return reconcile.Result{}, err
	}
	if err = topology.ValidateClickHouseObservedStatus(kdbInstance); err != nil {
		return reconcile.Result{}, err
	}

	// if the reconcile has been stopped,skip it
	kube.AbortWhen(rc.IsStopReconcile(), "instance is stop reconcile, skipped")(task)

	stepManager, err := NewInstanceStepManager(kdbInstance)
	if err != nil {
		return reconcile.Result{}, err
	}
	// activate the defer task for updating instance and status changes after all modifications are completed
	stepManager.PatchKDBInstanceStatus()(task, true)
	stepManager.PatchKDBInstance()(task, true)

	// Check for and handle deletion of cluster.
	kube.AbortWhen(rc.IsDeleted(), "instance is deleted, skipped")(task)
	kube.Branch(rc.IsDeleting(), stepManager.HandleDelete(), stepManager.CheckAndSetFinalizer())(task)
	stepManager.SetGlobalConfig()(task)
	stepManager.EnsureLeader()(task)
	stepManager.ReconcileLifecycle()(task)
	stepManager.SetInstanceConfig()(task)
	stepManager.SetRbac()(task)
	stepManager.SetService()(task)
	stepManager.SetMonitor()(task)
	stepManager.InitObservedRunner()(task)
	stepManager.ScaleUpInstance()(task)
	stepManager.ScaleDownInstance()(task)
	stepManager.ReconcileProxySQL()(task)
	result, err := kube.NewExecutor(logger).Execute(rc, task)
	if err == nil && naming.IsPGEngine(kdbInstance) && !result.Requeue && result.RequeueAfter == 0 {
		// Lease expiry is a passage-of-time event, not a Kubernetes watch event.
		// A short periodic reconcile lets the Operator perform external fencing
		// even when the expired holder and API server produce no further events.
		result.RequeueAfter = 5 * time.Second
	}
	return result, err
}

func NewInstanceStepManager(kdbInstance *v1.KDBInstance) (steps.InstanceStepper, error) {
	switch {
	case naming.IsMySQLEngine(kdbInstance):
		return &mysql.InstanceStepManager{}, nil
	case naming.IsClickHouseEngine(kdbInstance):
		return &clickhouse.InstanceStepManager{}, nil
	case naming.IsPGEngine(kdbInstance):
		return &pg.InstanceStepManager{}, nil
	default:
		return nil, fmt.Errorf("unsupported instance engine: %s", naming.Engine(kdbInstance))
	}
}

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=endpoints,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=delete;get;list;patch;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=create;get;list;patch;update;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;patch;update;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=create;get;list;patch;update;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=create;get;list;patch;update;watch
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors;prometheusrules,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=create;delete;get;list;patch;update;watch

// SetupWithManager adds the KDBInstance controller to the provided runtime manager
func (r *KDBInstanceReconciler) SetupWithManager(mgr manager.Manager) error {
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
		For(&v1.KDBInstance{}).
		WithOptions(opts).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Endpoints{}).
		Owns(&discoveryv1.EndpointSlice{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&batchv1.Job{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		// A physical restore using AdoptSource must follow the source
		// credential Secret.  The source owns that Secret, so Owns(Secret)
		// alone would only enqueue the source instance; this explicit mapping
		// also enqueues target instances that reference the source.
		Watches(&source.Kind{Type: &corev1.Secret{}}, handler.EnqueueRequestsFromMapFunc(mysqlRestoreCredentialRequests(mgr.GetClient()))).
		Watches(&source.Kind{Type: &coordinationv1.Lease{}}, handler.EnqueueRequestsFromMapFunc(postgreSQLDCSRequests)).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, handler.EnqueueRequestsFromMapFunc(postgreSQLDCSRequests)).
		Complete(r)
}

// mysqlRestoreCredentialRequests maps a source MySQL credential Secret update
// to every namespace-local KDBInstance that explicitly adopts that source's
// credential during a physical restore.  The map is deliberately closed over
// the cache client instead of using an unbounded enqueue-all handler: source
// credential rotation should not cause unrelated databases to reconcile.
func mysqlRestoreCredentialRequests(cache client.Client) handler.MapFunc {
	return func(object client.Object) []reconcile.Request {
		if cache == nil || object == nil {
			return nil
		}
		const suffix = "-mysql-credential"
		secretName := object.GetName()
		if !strings.HasSuffix(secretName, suffix) {
			return nil
		}
		sourceName := strings.TrimSuffix(secretName, suffix)
		if sourceName == "" {
			return nil
		}
		instances := &v1.KDBInstanceList{}
		if err := cache.List(context.Background(), instances, client.InNamespace(object.GetNamespace())); err != nil {
			// Event handlers cannot surface an error to the queue.  The next
			// source Secret update or normal periodic reconciliation will retry;
			// never enqueue unrelated instances on a cache failure.
			return nil
		}
		requests := make([]reconcile.Request, 0)
		for index := range instances.Items {
			instance := &instances.Items[index]
			if instance.Spec.MySQL == nil || instance.Spec.MySQL.Restore == nil {
				continue
			}
			restore := instance.Spec.MySQL.Restore
			if restore.CredentialMode != v1.MySQLRestoreCredentialModeAdoptSource ||
				restore.SourceInstanceRef == nil ||
				strings.TrimSpace(restore.SourceInstanceRef.Name) != sourceName ||
				instance.Name == sourceName {
				continue
			}
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)})
		}
		return requests
	}
}

func postgreSQLDCSRequests(object client.Object) []reconcile.Request {
	scope := object.GetLabels()["kdb.com/dcs-scope"]
	if scope == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: object.GetNamespace(), Name: scope}}}
}
