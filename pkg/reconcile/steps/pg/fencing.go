package pg

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	reconcilecontext "github.com/sqc157400661/kdb/pkg/reconcile/context"
)

const (
	postgresqlFencedTermAnnotation = "kdb.com/fenced-term"
	postgresqlPodUIDAnnotation     = "kdb.com/pod-uid"
)

// reconcilePostgreSQLExternalFence is the Operator-owned hard-fencing
// extension point. kdb-ha may only advance to term+1 after this controller has
// observed the expired holder Pod gone (or deleted it and later observed it
// gone) and CAS-patched the Lease confirmation.
func reconcilePostgreSQLExternalFence(rc *reconcilecontext.InstanceContext, instance *v1.KDBInstance) (bool, error) {
	lease := &coordinationv1.Lease{}
	key := client.ObjectKey{Namespace: instance.Namespace, Name: naming.PostgreSQLLeaderLeaseName(instance.Name)}
	if err := rc.Client().Get(rc.Context(), key, lease); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if !postgresqlLeaseExpired(lease, time.Now()) || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
		return false, nil
	}
	term := int32(1)
	if lease.Spec.LeaseTransitions != nil && *lease.Spec.LeaseTransitions > 0 {
		term = *lease.Spec.LeaseTransitions
	}
	if lease.Annotations[postgresqlFencedTermAnnotation] == strconv.FormatInt(int64(term), 10) {
		return false, nil
	}

	podName, holderUID := postgreSQLHolderIdentity(*lease.Spec.HolderIdentity)
	if annotationUID := lease.Annotations[postgresqlPodUIDAnnotation]; annotationUID != "" {
		holderUID = annotationUID
	}
	if podName == "" {
		return false, fmt.Errorf("expired PostgreSQL leader Lease %s has invalid holderIdentity", lease.Name)
	}
	pod := &corev1.Pod{}
	err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: podName}, pod)
	switch {
	case apierrors.IsNotFound(err):
		return confirmPostgreSQLFencedTerm(rc, lease, term)
	case err != nil:
		return false, err
	case holderUID != "" && string(pod.UID) != holderUID:
		// A same-name Pod with a different UID is not the expired holder and
		// must never be deleted. Its predecessor is already externally fenced.
		return confirmPostgreSQLFencedTerm(rc, lease, term)
	case pod.Labels[naming.LabelInstance] != instance.Name:
		return false, fmt.Errorf("refusing to fence Pod %s not owned by PostgreSQL instance %s", pod.Name, instance.Name)
	default:
		if err := rc.Client().Delete(rc.Context(), pod); err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		return true, nil
	}
}

func confirmPostgreSQLFencedTerm(rc *reconcilecontext.InstanceContext, lease *coordinationv1.Lease, term int32) (bool, error) {
	before := lease.DeepCopy()
	if lease.Annotations == nil {
		lease.Annotations = map[string]string{}
	}
	lease.Annotations[postgresqlFencedTermAnnotation] = strconv.FormatInt(int64(term), 10)
	if err := rc.Client().Patch(rc.Context(), lease, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return false, err
	}
	return true, nil
}

func postgresqlLeaseExpired(lease *coordinationv1.Lease, now time.Time) bool {
	if lease == nil || lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return false
	}
	expiresAt := lease.Spec.RenewTime.Time.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
	return !expiresAt.After(now)
}

func postgreSQLHolderIdentity(identity string) (name, uid string) {
	name, uid, _ = strings.Cut(identity, "/")
	return name, uid
}

func newPostgreSQLLeaderLease(name, namespace, holder, uid string, term, ttl int32, renewTime time.Time) *coordinationv1.Lease {
	return &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace,
			Annotations: map[string]string{postgresqlPodUIDAnnotation: uid},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity: &holder, LeaseDurationSeconds: &ttl, LeaseTransitions: &term,
			RenewTime: &metav1.MicroTime{Time: renewTime},
		},
	}
}
