package context

import (
	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/mysql.kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type InstanceContext struct {
	// base reconcileContext
	kube.ReconcileContext

	oldInstance *v1.KDBInstance
	instance    *v1.KDBInstance
	// config
	globalConfig map[string][]byte

	clusterConfigMap *corev1.ConfigMap
}

func NewInstanceContext(base kube.ReconcileContext) *InstanceContext {
	return &InstanceContext{
		ReconcileContext: base,
	}
}

// InitInstance initialize instance
func (rc *InstanceContext) InitInstance() (*v1.KDBInstance, error) {
	if rc.instance != nil {
		return rc.instance, nil
	}
	// get the postgrescluster from the cache
	instance := &v1.KDBInstance{}
	if err := rc.Client().Get(rc.Context(), rc.Request().NamespacedName, instance); err != nil {
		// NotFound cannot be fixed by requeuing so ignore it. During background
		// deletion, we receive delete events from cluster's dependents after
		// cluster is deleted.
		if err = client.IgnoreNotFound(err); err != nil {
			err = errors.Wrap(err, "unable to fetch PostgresCluster")
		}
		return nil, err
	}
	// Set any defaults that may not have been stored in the API. No DeepCopy
	// is necessary because controller-runtime makes a copy before returning
	// from its cache.
	// instance.Default()
	rc.oldInstance = instance.DeepCopy()
	rc.instance = instance

	if instance.Annotations == nil {
		instance.Annotations = make(map[string]string)
	}
	return rc.instance, nil
}

// GetOldInstance get the instance object before changed
func (rc *InstanceContext) GetOldInstance() *v1.KDBInstance {
	return rc.oldInstance
}

// GetInstance get current instance object
func (rc *InstanceContext) GetInstance() *v1.KDBInstance {
	return rc.instance
}

// IsDeleting The cluster is being deleted and our finalizer is still set.
func (rc *InstanceContext) IsDeleting() bool {
	// An object with Finalizers does not go away when deleted in the Kubernetes
	// API. Instead, it is given a DeletionTimestamp so that controllers can
	// react before it goes away. The object will remain in this state until
	// its Finalizers list is empty. Controllers are expected to remove their
	// finalizer from this list when they have completed their work.
	// - https://docs.k8s.io/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#finalizers
	// - https://book.kubebuilder.io/reference/using-finalizers.html
	if !rc.instance.DeletionTimestamp.IsZero() && rc.HasFinalizer(naming.Finalizer) {
		return true
	}
	return false
}

// IsDeleted The instance is being deleted and there is no finalizer.
func (rc *InstanceContext) IsDeleted() bool {
	if rc.instance.DeletionTimestamp != nil && !rc.instance.DeletionTimestamp.IsZero() && !rc.HasFinalizer(naming.Finalizer) {
		return true
	}
	return false
}

// IsStopReconcile is the cluster stop reconcile
func (rc *InstanceContext) IsStopReconcile() bool {
	if rc.instance != nil && rc.instance.Annotations != nil {
		if rc.instance.Annotations[naming.StopReconcile] == "true" {
			return true
		}
	}
	return false
}

// HasFinalizer determine if the finalizer exists
func (rc *InstanceContext) HasFinalizer(key string) bool {
	finalizers := sets.NewString(rc.instance.Finalizers...)
	return finalizers.Has(key)
}

// PatchKDBInstanceStatus the function for the updating the PostgresCluster status. Returns any error that
// occurs while attempting to patch the status
func (rc *InstanceContext) PatchKDBInstanceStatus() error {
	if !equality.Semantic.DeepEqual(rc.oldInstance.Status, rc.instance.Status) {
		// NOTE: Kubernetes prior to v1.16.10 and v1.17.6 does not track
		// managed fields on the status subresource: https://issue.k8s.io/88901
		if err := errors.WithStack(rc.Client().Status().Patch(
			rc.Context(), rc.instance, client.MergeFrom(rc.oldInstance), rc.Owner())); err != nil {
			return err
		}
	}
	return nil
}

// PatchKDBInstance the function for the updating the mysql instance. Returns any error that
// occurs while attempting to patch the instance
func (rc *InstanceContext) PatchKDBInstance() error {
	before := rc.GetOldInstance()
	instance := rc.GetInstance()
	intent := instance.DeepCopy()
	if equality.Semantic.DeepEqual(intent.Spec, before.Spec) &&
		equality.Semantic.DeepEqual(intent.ObjectMeta.Labels, before.ObjectMeta.Labels) &&
		equality.Semantic.DeepEqual(intent.ObjectMeta.Annotations, before.ObjectMeta.Annotations) {
		return nil
	}
	// not support server-side apply
	return rc.Client().Patch(rc.Context(), intent, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}
