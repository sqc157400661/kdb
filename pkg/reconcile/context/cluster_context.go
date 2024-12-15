package context

import (
	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/config"
	"github.com/sqc157400661/kdb/internal/naming"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ClusterContext struct {
	// base reconcileContext
	kube.ReconcileContext

	oldCluster *v1.KDBCluster
	cluster    *v1.KDBCluster
	// config
	globalConfig *config.GlobalConfig
}

func NewClusterContext(base kube.ReconcileContext) *ClusterContext {
	return &ClusterContext{
		ReconcileContext: base,
	}
}

func (rc *ClusterContext) SetGlobalConfig(config *config.GlobalConfig) {
	rc.globalConfig = config
}

func (rc *ClusterContext) GetGlobalConfig() config.GlobalConfig {
	if rc.globalConfig == nil {
		return config.GlobalConfig{}
	}
	return *rc.globalConfig
}

// InitCluster initialize instance
func (rc *ClusterContext) InitCluster() (*v1.KDBCluster, error) {
	if rc.cluster != nil {
		return rc.cluster, nil
	}
	// get the kdb instance from the cache
	cluster := &v1.KDBCluster{}
	if err := rc.Client().Get(rc.Context(), rc.Request().NamespacedName, cluster); err != nil {
		// NotFound cannot be fixed by requesting so ignore it. During background
		// deletion, we receive delete events from cluster's dependents after
		// cluster is deleted.
		if err = client.IgnoreNotFound(err); err != nil {
			err = errors.Wrap(err, "unable to fetch KDBInstance")
		}
		return nil, err
	}
	// Set any defaults that may not have been stored in the API. No DeepCopy
	// is necessary because controller-runtime makes a copy before returning
	// from its cache.
	// instance.Default()
	rc.oldCluster = cluster.DeepCopy()
	rc.cluster = cluster

	if cluster.Annotations == nil {
		cluster.Annotations = make(map[string]string)
	}
	return rc.cluster, nil
}

// GetOldInstance get the instance object before changed
func (rc *ClusterContext) GetOldInstance() *v1.KDBCluster {
	return rc.oldCluster
}

// GetInstance get current instance object
func (rc *ClusterContext) GetInstance() *v1.KDBCluster {
	return rc.cluster
}

// IsDeleted The instance is being deleted and there is no finalizer.
func (rc *ClusterContext) IsDeleted() bool {
	if rc.cluster.DeletionTimestamp != nil && !rc.cluster.DeletionTimestamp.IsZero() && !rc.HasFinalizer(naming.Finalizer) {
		return true
	}
	return false
}

// IsStopReconcile is the cluster stop reconcile
func (rc *ClusterContext) IsStopReconcile() bool {
	if rc.cluster != nil && rc.cluster.Annotations != nil {
		if rc.cluster.Annotations[naming.StopReconcile] == "true" {
			return true
		}
	}
	return false
}

// HasFinalizer determine if the finalizer exists
func (rc *ClusterContext) HasFinalizer(key string) bool {
	finalizers := sets.NewString(rc.cluster.Finalizers...)
	return finalizers.Has(key)
}

// DeleteFinalizer delete finalizer
func (rc *ClusterContext) DeleteFinalizer(key string) []string {
	finalizers := sets.NewString(rc.cluster.Finalizers...)
	finalizers.Delete(key)
	return finalizers.List()
}
