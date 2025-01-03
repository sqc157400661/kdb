package observed

import (
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

type ObservedCluster struct {
	Items  []*v1.KDBInstance
	ByName map[string]*v1.KDBInstance
	Ready  int
}

func (i ObservedCluster) AddInstance(instance *v1.KDBInstance) {
	if instance == nil {
		return
	}
	if i.ByName == nil {
		i.ByName = map[string]*v1.KDBInstance{}
	}
	i.ByName[instance.Name] = instance
}

func (i ObservedCluster) GetInstanceByName(instanceName string) *v1.KDBInstance {
	if i.ByName == nil {
		return nil
	}
	return i.ByName[instanceName]
}

// SingleRunner represents a single KDB instance.
type SingleRunner struct {
	Name   string
	Pods   []*corev1.Pod
	Runner *appsv1.StatefulSet
}

// PodMatchesPodTemplate returns whether or not the Pod for this instance
// matches its specified PodTemplate. When it does not match, the Pod needs to
// be redeployed.
func (i SingleRunner) PodMatchesPodTemplate() (matches bool, known bool) {
	if i.Runner == nil || len(i.Pods) != 1 {
		return false, false
	}

	if i.Runner.Status.ObservedGeneration != i.Runner.Generation {
		return false, false
	}

	// When the Status is up-to-date, compare the revision of the Pod to that
	// of the PodTemplate.
	podRevision := i.Pods[0].Labels[appsv1.StatefulSetRevisionLabel]
	return podRevision == i.Runner.Status.UpdateRevision, true
}

// ObservedRunner represents the KDB instance.
type ObservedRunner struct {
	List     []*SingleRunner          // by instance name
	BySet    map[string]*SingleRunner // by StatefulSet name
	SetNames sets.String
}

// NewObservedRunner builds an ObservedSingleInstance from Kubernetes API objects.
func NewObservedRunner(
	instance *v1.KDBInstance,
	runners []appsv1.StatefulSet,
	pods []corev1.Pod,
) *ObservedRunner {
	setNum := *instance.Spec.InstanceSet.Replicas
	observed := ObservedRunner{
		BySet:    make(map[string]*SingleRunner, setNum),
		SetNames: sets.NewString(),
	}
	for i := 0; i < int(setNum); i++ {
		name := naming.InstanceStatefulSetName(instance.Name, i)
		observed.SetNames.Insert(name)
	}

	for i := range runners {
		ri := runners[i].Name
		singleInstance := &SingleRunner{
			Name:   ri,
			Runner: &runners[i],
		}
		observed.List = append(observed.List, singleInstance)
		observed.BySet[ri] = singleInstance
	}

	for i := range pods {
		ps := pods[i].Labels[naming.LabelInstanceSet]
		singleInstance := observed.BySet[ps]
		if singleInstance == nil {
			singleInstance = &SingleRunner{
				Name: ps,
			}
			observed.SetNames.Insert(ps)
			observed.List = append(observed.List, singleInstance)
			observed.BySet[ps] = singleInstance
		}
		singleInstance.Pods = append(singleInstance.Pods, &pods[i])
	}

	return &observed
}
