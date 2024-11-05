package observed

import (
	v1 "github.com/sqc157400661/kdb/apis/mysql.kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// SingleInstance represents a single KDB instance.
type SingleInstance struct {
	Name   string
	Pods   []*corev1.Pod
	Runner *appsv1.StatefulSet
}

// PodMatchesPodTemplate returns whether or not the Pod for this instance
// matches its specified PodTemplate. When it does not match, the Pod needs to
// be redeployed.
func (i SingleInstance) PodMatchesPodTemplate() (matches bool, known bool) {
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

// ObservedSingleInstance represents the KDB instance.
type ObservedSingleInstance struct {
	List     []*SingleInstance          // by instance name
	BySet    map[string]*SingleInstance // by StatefulSet name
	SetNames sets.String
}

// NewObservedSingleInstance builds an ObservedSingleInstance from Kubernetes API objects.
func NewObservedSingleInstance(
	instance *v1.KDBInstance,
	runners []appsv1.StatefulSet,
	pods []corev1.Pod,
) *ObservedSingleInstance {
	setNum := *instance.Spec.InstanceSet.Replicas
	observed := ObservedSingleInstance{
		List:     make([]*SingleInstance, setNum),
		BySet:    make(map[string]*SingleInstance, setNum),
		SetNames: sets.NewString(),
	}
	for i := 0; i < int(setNum); i++ {
		name := naming.InstanceStatefulSetName(instance.Name, i)
		observed.SetNames.Insert(name)
	}

	for i := range runners {
		ri := runners[i].Name
		singleInstance := &SingleInstance{
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
			singleInstance = &SingleInstance{
				Name: ps,
			}
			observed.SetNames.Insert(ps)
		}
		singleInstance.Pods = append(singleInstance.Pods, &pods[i])
	}

	return &observed
}
