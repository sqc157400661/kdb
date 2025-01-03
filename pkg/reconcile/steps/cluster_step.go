package steps

import (
	"fmt"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/config"
	"github.com/sqc157400661/kdb/internal/generate"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sort"
)

type Condition func(rc *context.ClusterContext, log logr.Logger) (bool, error)
type Step func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error)

type ClusterStepper interface {
	StepBinder(name string, f StepFunc) kube.BindFunc
	StepIfBinder(conditionName string, condFunc ConditionFunc, binders ...kube.BindFunc) kube.BindFunc
	CheckAndSetFinalizer() kube.BindFunc
	HandleDelete() kube.BindFunc
	SetGlobalConfig() kube.BindFunc
	SetInstanceConfig() kube.BindFunc
	ScaleUp() kube.BindFunc
	ScaleDown() kube.BindFunc
}

type ClusterStepManager struct {
}

// StepBinder bind one step to a task function
func (s *ClusterStepManager) StepBinder(name string, f Step) kube.BindFunc {
	return kube.NewStepBinder(
		kube.NewStep(
			name, func(rc kube.ReconcileContext, flow kube.Flow) (reconcile.Result, error) {
				return f(rc.(*context.ClusterContext), flow)
			},
		),
	)
}

// StepIfBinder bind one condition step to a task function
func (s *ClusterStepManager) StepIfBinder(conditionName string, condFunc Condition, binders ...kube.BindFunc) kube.BindFunc {
	condition := kube.NewCachedCondition(
		kube.NewCondition(conditionName, func(rc kube.ReconcileContext, log logr.Logger) (bool, error) {
			return condFunc(rc.(*context.ClusterContext), log)
		}),
	)

	ifBinders := make([]kube.BindFunc, len(binders))
	for i := range binders {
		ifBinders[i] = kube.NewStepIfBinder(condition, kube.ExtractStepsFromBindFunc(binders[i])[0])
	}

	return kube.CombineBinders(ifBinders...)
}

// CheckAndSetFinalizer check if the Finalizer exists, if not, add it
func (s *ClusterStepManager) CheckAndSetFinalizer() kube.BindFunc {
	return s.StepBinder(
		"CheckAndSetFinalizer",
		func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
			if !rc.IsDeleted() && !rc.IsDeleting() {
				if rc.HasFinalizer(naming.Finalizer) {
					return flow.Pass()
				}
				// The cluster is not being deleted and needs a finalizer; set it.

				// The Finalizers field is shared by multiple controllers, but the
				// server-side merge strategy does not work on our custom resource due
				// to a bug in Kubernetes. Build a merge-patch that includes the full
				// list of Finalizers plus ResourceVersion to detect conflicts with
				// other potential writers.
				// - https://issue.k8s.io/99730
				before := rc.GetCluster().DeepCopy()
				// Make another copy so that Patch doesn't write back to cluster.
				intent := before.DeepCopy()
				intent.Finalizers = append(intent.Finalizers, naming.Finalizer)
				err := errors.WithStack(rc.Patch(intent,
					client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})))
				if err != nil {
					return flow.Error(err, "patch finalizers error")
				}
			}
			return flow.Pass()
		})
}

// HandleDelete sets a finalizer on cluster and performs the finalization of
// cluster when it is being deleted. It returns (nil, nil) when cluster is
// not being deleted. The caller is responsible for returning other values to
// controller-runtime.
func (s *ClusterStepManager) HandleDelete() kube.BindFunc {
	return s.StepBinder(
		"HandleDelete",
		func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
			// Find all instance pods to determine which to shut down and in what order.
			pods := &corev1.PodList{}
			instances, err := naming.AsSelector(naming.KDBInstance(rc.Name()))
			if err == nil {
				err = errors.WithStack(rc.List(pods, instances))
			}
			if err != nil {
				return flow.Error(err, "get pod list err")
			}
			return flow.Break("deleting")
		})
}

// SetGlobalConfig including configuration information such as Root certificate, username and password
func (s *ClusterStepManager) SetGlobalConfig() kube.BindFunc {
	return s.StepBinder(
		"SetGlobalConfig",
		func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetCluster()
			existing := &corev1.Secret{}
			existing.Namespace, existing.Name = instance.Namespace, naming.GlobalConfigSecret
			err := errors.WithStack(client.IgnoreNotFound(rc.Get(existing)))
			if err != nil {
				return flow.Error(err, "get GlobalConfig err")
			}
			if len(existing.Data) == 0 {
				return flow.Error(errors.New("GlobalConfig not exist"), "get GlobalConfig err")
			}
			globalConf := existing.Data[naming.GlobalConfigSecretKey]
			if len(globalConf) == 0 {
				return flow.Pass()
			}
			var conf config.GlobalConfig
			err = json.Unmarshal(globalConf, &conf)
			if err != nil {
				return flow.Error(errors.New("Unmarshal err"), err.Error())
			}
			rc.SetGlobalConfig(&conf)
			return flow.Pass()
		})
}

func (s *ClusterStepManager) InitObservedInstance() kube.BindFunc {
	return s.StepBinder(
		"InitObservedInstances",
		func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
			instances := &v1.KDBInstanceList{}
			selector, err := naming.AsSelector(naming.KDBCluster(rc.Name()))
			if err != nil {
				return flow.Error(err, "get selector err")
			}
			err = rc.List(instances, selector)
			if err != nil {
				return flow.Error(err, "get instance list err")
			}
			rc.InitObservedCluster(instances)
			return flow.Pass()
		})
}

func (s *ClusterStepManager) ScaleUp() kube.BindFunc {
	return s.StepBinder(
		"ScaleUp",
		func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
			cluster := rc.GetCluster()
			observedCluster := rc.GetObservedCluster()
			instances := observedCluster.Items
			existInstanceNames := sets.NewString()
			for _, instance := range instances {
				existInstanceNames.Insert(instance.Name)
			}
			masters, err := picMasterInstances(rc)
			if err != nil {
				return flow.Error(err, "picMasterInstances err")
			}
			for _, ins := range cluster.Spec.Instances {
				if !existInstanceNames.Has(ins.Name) {
					observedCluster.AddInstance(&v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{
						Namespace: cluster.Namespace,
						Name:      ins.Name,
					}})
				}
				err = generate.InitKDBInstance(rc, observedCluster.GetInstanceByName(ins.Name), &ins, masters)
				if err != nil {
					return flow.Error(err, "reconcileInstance err")
				}
			}
			return flow.Pass()
		})
}

func (s *ClusterStepManager) ScaleDown() kube.BindFunc {
	return s.StepBinder(
		"ScaleDown",
		func(rc *context.ClusterContext, flow kube.Flow) (reconcile.Result, error) {
			observedCluster := rc.GetObservedCluster()
			keepNames := getInsNamesNeedToKeep(rc)
			for _, ins := range observedCluster.Items {
				if !keepNames.Has(ins.Name) {
					err := deleteInstance(rc, ins.Name)
					if err != nil {
						return flow.Error(err, "deleteInstance err")
					}
				}
			}
			return flow.Pass()
		})
}

// deleteSts will delete all resources related to a single sts
func deleteInstance(rc *context.ClusterContext, insName string) error {
	ins := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: insName}}
	err := errors.WithStack(client.IgnoreNotFound(rc.Client().Delete(rc.Context(), ins)))
	if client.IgnoreNotFound(err) != nil {
		return err
	}
	return err
}

func getInsNamesNeedToKeep(rc *context.ClusterContext) sets.String {
	cluster := rc.GetCluster()
	namesToKeep := sets.NewString()
	for _, ins := range cluster.Spec.Instances {
		namesToKeep.Insert(ins.Name)
	}
	// TODO: 如果Cluster层先删除了Master如何处理？
	return namesToKeep
}

func picMasterInstances(rc *context.ClusterContext) (masters []*v1.HostInfo, err error) {
	cluster := rc.GetCluster()
	if !naming.IsMasterSlaveCluster(cluster) {
		return
	}
	if cluster.Spec.DeployArch == naming.MySQLMasterReplicaDeployArch {
		return picMasterReplicaMasters(&cluster.Spec)
	}
	if cluster.Spec.DeployArch == naming.MySQLMasterSlaveDeployArch {
		return picMasterSlaveMasters(&cluster.Spec)
	}
	return
}

// picMasterReplicaMasters determines the master replicas for a KDBCluster based on the given specification.
// It validates the Leader.PodName and Instances in the spec, and then returns the appropriate master pod names.
func picMasterReplicaMasters(spec *v1.KDBClusterSpec) (masters []*v1.HostInfo, err error) {
	// 1. Validate that Leader.PodName must be empty
	if !naming.IsEmptyLeader(spec.Leader) {
		return nil, fmt.Errorf("when DeployArch is %s, Leader.PodName must be empty", naming.MySQLMasterReplicaDeployArch)
	}

	// 2. Validate that the length of Instances must be greater than 1
	if len(spec.Instances) <= 1 {
		return nil, fmt.Errorf("when DeployArch is %s, len(Instances) must be greater than 1", naming.MySQLMasterReplicaDeployArch)
	}

	// 3. If the length of Instances is 2, return the podName slice of Instances
	if len(spec.Instances) == 2 {
		for _, instance := range spec.Instances {
			masters = append(masters, &v1.HostInfo{
				PodName: naming.InstancePodName(instance.Name, 0),
			})
		}
		return masters, nil
	}

	// 4. If the length of Instances is greater than 2, return the pod names of the two instances with the largest CPU requirements
	if len(spec.Instances) > 2 {
		// Sort Instances based on CPU resource requests
		sort.Slice(spec.Instances, func(i, j int) bool {
			cpuI := spec.Instances[i].Resources.Requests[corev1.ResourceCPU]
			cpuJ := spec.Instances[j].Resources.Requests[corev1.ResourceCPU]
			return cpuI.Cmp(cpuJ) > 0 // 降序排序
		})

		// Get the pod names of the two instances with the highest CPU requests
		for i := 0; i < 2; i++ {
			masters = append(masters, &v1.HostInfo{
				PodName: naming.InstancePodName(spec.Instances[i].Name, 0),
			})
		}
	}
	return
}

// picMasterSlaveMasters determines the master slaves for a KDBCluster based on the given specification.
func picMasterSlaveMasters(spec *v1.KDBClusterSpec) (masters []*v1.HostInfo, err error) {
	if !naming.IsEmptyLeader(spec.Leader) {
		return []*v1.HostInfo{&spec.Leader}, nil
	}
	// Sort Instances based on CPU resource requests
	sort.Slice(spec.Instances, func(i, j int) bool {
		cpuI := spec.Instances[i].Resources.Requests[corev1.ResourceCPU]
		cpuJ := spec.Instances[j].Resources.Requests[corev1.ResourceCPU]
		return cpuI.Cmp(cpuJ) > 0 // 降序排序
	})
	masters = append(masters, &v1.HostInfo{
		PodName: naming.InstancePodName(spec.Instances[0].Name, 0),
	})
	return
}
