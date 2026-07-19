package pg

import (
	stdctx "context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/postgresqlruntime"
	reconcilecontext "github.com/sqc157400661/kdb/pkg/reconcile/context"
	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type postgreSQLNativeStatus struct {
	Name           string                    `json:"name"`
	Scope          string                    `json:"scope"`
	Role           string                    `json:"role"`
	Running        bool                      `json:"running"`
	Timeline       int                       `json:"timeline"`
	LSN            string                    `json:"lsn"`
	Leader         string                    `json:"leader"`
	Term           int64                     `json:"term"`
	Synchronous    bool                      `json:"synchronous"`
	LeaseExpiresAt time.Time                 `json:"leaseExpiresAt"`
	DR             *postgreSQLNativeDRStatus `json:"dr,omitempty"`
}

type postgreSQLNativeDRStatus struct {
	Enabled             bool                                   `json:"enabled"`
	ClusterID           string                                 `json:"clusterId"`
	PeerClusterID       string                                 `json:"peerClusterId"`
	ConfiguredRole      string                                 `json:"configuredRole"`
	RuntimeRole         string                                 `json:"runtimeRole"`
	ActiveClusterID     string                                 `json:"activeClusterId"`
	Term                int64                                  `json:"term"`
	FencedClusterID     string                                 `json:"fencedClusterId"`
	FencedTerm          int64                                  `json:"fencedTerm"`
	Connected           bool                                   `json:"connected"`
	ManualPromotionOnly bool                                   `json:"manualPromotionOnly"`
	LastOperationID     string                                 `json:"lastOperationId"`
	LastPromotedAt      *time.Time                             `json:"lastPromotedAt"`
	LastFencedAt        *time.Time                             `json:"lastFencedAt"`
	ClusterHeartbeats   map[string]postgreSQLNativeDRHeartbeat `json:"clusterHeartbeats"`
}

type postgreSQLNativeDRHeartbeat struct {
	ClusterID  string    `json:"clusterId"`
	Role       string    `json:"role"`
	LSN        string    `json:"lsn"`
	ObservedAt time.Time `json:"observedAt"`
}

type postgreSQLNativeConfig struct {
	Revision int64          `json:"revision"`
	Config   map[string]any `json:"config"`
}

var fetchPostgreSQLRuntimeStatus = func(ctx stdctx.Context, endpoint string, httpClient *http.Client, username, password string) (postgreSQLNativeStatus, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/postgresql/status", nil)
	if err != nil {
		return postgreSQLNativeStatus{}, err
	}
	request.SetBasicAuth(username, password)
	response, err := httpClient.Do(request)
	if err != nil {
		return postgreSQLNativeStatus{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return postgreSQLNativeStatus{}, fmt.Errorf("kdb-ha status returned %s", response.Status)
	}
	var status postgreSQLNativeStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return postgreSQLNativeStatus{}, err
	}
	return status, nil
}

var fetchPostgreSQLDynamicConfig = func(ctx stdctx.Context, endpoint string, httpClient *http.Client, username, password string) (postgreSQLNativeConfig, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/postgresql/config", nil)
	if err != nil {
		return postgreSQLNativeConfig{}, err
	}
	request.SetBasicAuth(username, password)
	response, err := httpClient.Do(request)
	if err != nil {
		return postgreSQLNativeConfig{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return postgreSQLNativeConfig{}, fmt.Errorf("kdb-ha config returned %s", response.Status)
	}
	var value postgreSQLNativeConfig
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return postgreSQLNativeConfig{}, err
	}
	return value, nil
}

func observePostgreSQLRuntime(rc *reconcilecontext.InstanceContext, instance *v1.KDBInstance, pods []corev1.Pod) (*v1.PostgreSQLStatus, map[string]string, error) {
	status := &v1.PostgreSQLStatus{Services: postgreSQLServiceNames(instance)}
	if instance.Status.PostgreSQL != nil {
		status.BootstrapParameters = instance.Status.PostgreSQL.BootstrapParameters
		status.DynamicConfigRevision = instance.Status.PostgreSQL.DynamicConfigRevision
		status.EffectiveConfigHash = instance.Status.PostgreSQL.EffectiveConfigHash
		status.Lifecycle = instance.Status.PostgreSQL.Lifecycle
		status.DR = instance.Status.PostgreSQL.DR
	}
	roles := make(map[string]string)
	lease := &coordinationv1.Lease{}
	if err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: naming.PostgreSQLLeaderLeaseName(instance.Name)}, lease); err != nil {
		if apierrors.IsNotFound(err) {
			setPostgreSQLConditions(instance, status, len(pods), 0)
			return status, roles, nil
		}
		return nil, nil, err
	}
	if postgresqlLeaseExpired(lease, time.Now()) || lease.Spec.HolderIdentity == nil {
		setPostgreSQLConditions(instance, status, len(pods), 0)
		return status, roles, nil
	}
	term := int64(1)
	if lease.Spec.LeaseTransitions != nil && *lease.Spec.LeaseTransitions > 0 {
		term = int64(*lease.Spec.LeaseTransitions)
	}
	status.Term = term
	runtimeClient, username, password, err := postgresqlruntime.Client(rc.Context(), rc.Client(), instance, 750*time.Millisecond)
	if err != nil {
		return nil, nil, err
	}
	leaderName, leaderUID := postgreSQLHolderIdentity(*lease.Spec.HolderIdentity)
	if uid := lease.Annotations[postgresqlPodUIDAnnotation]; uid != "" {
		leaderUID = uid
	}
	zones := map[string]struct{}{}
	for i := range pods {
		pod := &pods[i]
		if pod.Status.PodIP == "" || !podReady(pod) {
			continue
		}
		runtimeStatus, err := fetchPostgreSQLRuntimeStatus(rc.Context(), postgresqlruntime.PodEndpoint(instance, pod.Name), runtimeClient, username, password)
		if err != nil || runtimeStatus.Name != pod.Name || runtimeStatus.Scope != instance.Name || !runtimeStatus.Running || runtimeStatus.Term != term || runtimeStatus.Leader != leaderName {
			continue
		}
		role := ""
		memberRole := runtimeStatus.Role
		switch {
		case pod.Name == leaderName && (leaderUID == "" || string(pod.UID) == leaderUID) && runtimeStatus.Role == "primary":
			role = naming.MasterRole
			status.Primary = pod.Name
		case pod.Name == leaderName && (leaderUID == "" || string(pod.UID) == leaderUID) && runtimeStatus.Role == "replica" && runtimeStatus.DR != nil && runtimeStatus.DR.RuntimeRole == "standby":
			role = naming.ReplicaRole
			memberRole = "standby_leader"
			status.Replicas = append(status.Replicas, pod.Name)
		case pod.Name != leaderName && runtimeStatus.Role == "replica":
			role = naming.ReplicaRole
			status.Replicas = append(status.Replicas, pod.Name)
		}
		if role == "" {
			continue
		}
		roles[pod.Name] = role
		zone := postgreSQLPodZone(rc, pod)
		if zone != "" {
			zones[zone] = struct{}{}
		}
		status.Members = append(status.Members, v1.PostgreSQLMemberStatus{
			Name: pod.Name, PodUID: string(pod.UID), Role: memberRole, Running: true, Ready: true,
			Synchronous: runtimeStatus.Synchronous, Timeline: runtimeStatus.Timeline, LSN: runtimeStatus.LSN, Term: term, NodeName: pod.Spec.NodeName,
			Zone: zone, ObservedAt: metav1.Now(),
		})
		if runtimeStatus.DR != nil && (status.DR == nil || pod.Name == leaderName) {
			status.DR = projectPostgreSQLDRStatusPreservingDrill(runtimeStatus.DR, status.DR)
		}
	}
	status.Ready = len(status.Members) == desiredPostgreSQLReplicas(instance) && (status.Primary != "" || status.DR != nil && status.DR.RuntimeRole == "standby")
	if status.Primary != "" {
		for i := range pods {
			if pods[i].Name != status.Primary || pods[i].Status.PodIP == "" {
				continue
			}
			if snapshot, err := fetchPostgreSQLDynamicConfig(rc.Context(), postgresqlruntime.PodEndpoint(instance, pods[i].Name), runtimeClient, username, password); err == nil {
				status.DynamicConfigRevision = snapshot.Revision
				encoded, _ := json.Marshal(snapshot.Config)
				hash := sha256.Sum256(encoded)
				status.EffectiveConfigHash = fmt.Sprintf("sha256:%x", hash[:])
			}
			break
		}
	}
	setPostgreSQLConditions(instance, status, len(pods), len(zones))
	return status, roles, nil
}

func projectPostgreSQLDRStatusPreservingDrill(runtime *postgreSQLNativeDRStatus, current *v1.PostgreSQLDRStatus) *v1.PostgreSQLDRStatus {
	projected := projectPostgreSQLDRStatus(runtime)
	if projected != nil && current != nil {
		// The drill result is written by the DBFailover controller and is not
		// part of Pod-local kdb-ha status. Keep the durable control-plane
		// observation while refreshing the live DR projection.
		projected.LastDrill = current.LastDrill
	}
	return projected
}

func projectPostgreSQLDRStatus(runtime *postgreSQLNativeDRStatus) *v1.PostgreSQLDRStatus {
	if runtime == nil {
		return nil
	}
	result := &v1.PostgreSQLDRStatus{
		Enabled: runtime.Enabled, ClusterID: runtime.ClusterID, PeerClusterID: runtime.PeerClusterID,
		ConfiguredRole: runtime.ConfiguredRole, RuntimeRole: runtime.RuntimeRole, ActiveClusterID: runtime.ActiveClusterID,
		Term: runtime.Term, FencedClusterID: runtime.FencedClusterID, FencedTerm: runtime.FencedTerm,
		Connected: runtime.Connected, ManualPromotionOnly: runtime.ManualPromotionOnly, LastOperationID: runtime.LastOperationID,
		ClusterHeartbeats: map[string]v1.PostgreSQLDRHeartbeat{},
	}
	if runtime.LastPromotedAt != nil {
		value := metav1.NewTime(*runtime.LastPromotedAt)
		result.LastPromotedAt = &value
	}
	if runtime.LastFencedAt != nil {
		value := metav1.NewTime(*runtime.LastFencedAt)
		result.LastFencedAt = &value
	}
	for id, heartbeat := range runtime.ClusterHeartbeats {
		result.ClusterHeartbeats[id] = v1.PostgreSQLDRHeartbeat{ClusterID: heartbeat.ClusterID, Role: heartbeat.Role, LSN: heartbeat.LSN, ObservedAt: metav1.NewTime(heartbeat.ObservedAt)}
	}
	active, activeOK := runtime.ClusterHeartbeats[runtime.ActiveClusterID]
	local, localOK := runtime.ClusterHeartbeats[runtime.ClusterID]
	if activeOK && localOK {
		if activeLSN, ok := parsePostgreSQLLSN(active.LSN); ok {
			if localLSN, ok := parsePostgreSQLLSN(local.LSN); ok && activeLSN > localLSN {
				result.RPOBytes = int64(activeLSN - localLSN)
			}
		}
		if active.ObservedAt.After(local.ObservedAt) {
			result.RPOSeconds = int64(active.ObservedAt.Sub(local.ObservedAt).Seconds())
		}
	}
	return result
}

func parsePostgreSQLLSN(value string) (uint64, bool) {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) != 2 {
		return 0, false
	}
	high, errHigh := strconv.ParseUint(parts[0], 16, 32)
	low, errLow := strconv.ParseUint(parts[1], 16, 32)
	if errHigh != nil || errLow != nil {
		return 0, false
	}
	return high<<32 | low, true
}

func reconcilePostgreSQLPodRoles(rc *reconcilecontext.InstanceContext, pods []corev1.Pod, roles map[string]string) error {
	for i := range pods {
		pod := &pods[i]
		before := pod.DeepCopy()
		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		if role := roles[pod.Name]; role != "" {
			pod.Labels[naming.LabelRole] = role
		} else {
			delete(pod.Labels, naming.LabelRole)
		}
		if before.Labels[naming.LabelRole] == pod.Labels[naming.LabelRole] {
			continue
		}
		if err := rc.Client().Patch(rc.Context(), pod, client.MergeFrom(before)); err != nil {
			return err
		}
	}
	return nil
}

func reconcilePostgreSQLEndpointSlices(rc *reconcilecontext.InstanceContext, instance *v1.KDBInstance, status *v1.PostgreSQLStatus, pods []corev1.Pod, roles map[string]string, port int32) error {
	writeEnabled := status == nil || status.DR == nil || status.DR.RuntimeRole == "active"
	services := map[string]func(string) bool{
		naming.InstanceReadWriteServiceName(instance.Name): func(role string) bool { return writeEnabled && role == naming.MasterRole },
		naming.InstanceReadOnlyServiceName(instance.Name):  func(role string) bool { return role == naming.ReplicaRole },
		naming.PostgreSQLAnyServiceName(instance.Name):     func(role string) bool { return role != "" },
	}
	protocol := corev1.ProtocolTCP
	for serviceName, include := range services {
		endpoints := make([]discoveryv1.Endpoint, 0, len(pods))
		for i := range pods {
			pod := &pods[i]
			if pod.Status.PodIP == "" || !include(roles[pod.Name]) {
				continue
			}
			ready := true
			endpoints = append(endpoints, discoveryv1.Endpoint{
				Addresses: []string{pod.Status.PodIP}, Conditions: discoveryv1.EndpointConditions{Ready: &ready},
				TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID},
				NodeName:  &pod.Spec.NodeName,
			})
		}
		slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{
			Name: serviceName, Namespace: instance.Namespace,
			Labels: map[string]string{discoveryv1.LabelServiceName: serviceName, naming.LabelInstance: instance.Name},
		}}
		slice.AddressType = discoveryv1.AddressTypeIPv4
		slice.Ports = []discoveryv1.EndpointPort{{Name: stringPtr(naming.PortDatabase), Protocol: &protocol, Port: &port}}
		if serviceName == naming.InstanceReadWriteServiceName(instance.Name) {
			haPort := int32(8008)
			slice.Ports = append(slice.Ports, discoveryv1.EndpointPort{Name: stringPtr(naming.PortPostgreSQLHA), Protocol: &protocol, Port: &haPort})
		}
		slice.Endpoints = endpoints
		slice.SetGroupVersionKind(discoveryv1.SchemeGroupVersion.WithKind("EndpointSlice"))
		if err := errors.WithStack(rc.SetControllerReference(slice)); err != nil {
			return err
		}
		if err := rc.Apply(slice); err != nil {
			return err
		}
	}
	return nil
}

func reconcilePostgreSQLPgBouncer(rc *reconcilecontext.InstanceContext, instance *v1.KDBInstance) error {
	if instance.Spec.PostgreSQL == nil || instance.Spec.PostgreSQL.PgBouncer == nil || !instance.Spec.PostgreSQL.PgBouncer.Enabled {
		return nil
	}
	spec := instance.Spec.PostgreSQL.PgBouncer
	name := naming.PostgreSQLPgBouncerName(instance.Name)
	replicas := int32(1)
	if spec.Replicas != nil {
		replicas = *spec.Replicas
	}
	port := spec.Port
	if port == 0 {
		port = 6432
	}
	image := spec.Image
	if image == "" {
		image = "edoburu/pgbouncer:1.23.1-p3"
	}
	labels := map[string]string{naming.LabelInstance: instance.Name, "app.kubernetes.io/component": "pgbouncer"}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: instance.Namespace}}
	deployment.Spec.Replicas = &replicas
	deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
	deployment.Spec.Template.ObjectMeta.Labels = labels
	deployment.Spec.Template.Spec.Containers = []corev1.Container{{
		Name: "pgbouncer", Image: image, Resources: spec.Resources,
		Ports: []corev1.ContainerPort{{Name: "pgbouncer", ContainerPort: port}},
		Env: []corev1.EnvVar{
			{Name: "DATABASE_URL", Value: fmt.Sprintf("postgres://%s.%s.svc:%d/postgres", naming.InstanceReadWriteServiceName(instance.Name), instance.Namespace, naming.KDBInstanceMasterPort(instance))},
			{Name: "LISTEN_PORT", Value: strconv.Itoa(int(port))},
		},
		ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(int(port))}}},
	}}
	deployment.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("Deployment"))
	if err := rc.SetControllerReference(deployment); err != nil {
		return err
	}
	if err := rc.Apply(deployment); err != nil {
		return err
	}
	service := newPostgreSQLService(instance, name, "", labels, port)
	service.Spec.Selector = labels
	service.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
	if err := rc.SetControllerReference(service); err != nil {
		return err
	}
	return rc.Apply(service)
}

func observePostgreSQLPgBouncer(rc *reconcilecontext.InstanceContext, instance *v1.KDBInstance) *v1.PostgreSQLPgBouncerStatus {
	if instance.Spec.PostgreSQL == nil || instance.Spec.PostgreSQL.PgBouncer == nil || !instance.Spec.PostgreSQL.PgBouncer.Enabled {
		return nil
	}
	name := naming.PostgreSQLPgBouncerName(instance.Name)
	deployment := &appsv1.Deployment{}
	_ = rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: name}, deployment)
	port := instance.Spec.PostgreSQL.PgBouncer.Port
	if port == 0 {
		port = 6432
	}
	return &v1.PostgreSQLPgBouncerStatus{
		Enabled: true, Replicas: valueOrOne(instance.Spec.PostgreSQL.PgBouncer.Replicas),
		ReadyReplicas: deployment.Status.ReadyReplicas, ServiceName: name, Port: port,
	}
}

func postgreSQLServiceNames(instance *v1.KDBInstance) v1.PostgreSQLServiceStatus {
	status := v1.PostgreSQLServiceStatus{
		Headless: naming.InstancePodServiceName(instance.Name), Primary: naming.InstanceReadWriteServiceName(instance.Name),
		Replicas: naming.InstanceReadOnlyServiceName(instance.Name), Any: naming.PostgreSQLAnyServiceName(instance.Name),
	}
	if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.PgBouncer != nil && instance.Spec.PostgreSQL.PgBouncer.Enabled {
		status.PgBouncer = naming.PostgreSQLPgBouncerName(instance.Name)
	}
	return status
}

func setPostgreSQLConditions(instance *v1.KDBInstance, status *v1.PostgreSQLStatus, observedPods, zones int) {
	desired := desiredPostgreSQLReplicas(instance)
	available := metav1.ConditionFalse
	availableReason := "PrimaryMissing"
	if status.Primary != "" {
		available, availableReason = metav1.ConditionTrue, "PrimaryLeaseValidated"
	}
	replication := metav1.ConditionFalse
	replicationReason := "ReplicasUnavailable"
	if status.Primary != "" && len(status.Replicas) >= maxInt(0, desired-1) {
		replication, replicationReason = metav1.ConditionTrue, "ReplicationMembersHealthy"
	}
	degraded := metav1.ConditionFalse
	degradedReason := "TopologySatisfiesPolicy"
	if desired >= 3 && observedPods >= desired && zones < desired {
		degraded, degradedReason = metav1.ConditionTrue, "InsufficientAvailabilityZones"
	}
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{Type: v1.PostgreSQLAvailable, Status: available, Reason: availableReason, ObservedGeneration: instance.Generation})
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{Type: v1.PostgreSQLReplicationHealthy, Status: replication, Reason: replicationReason, ObservedGeneration: instance.Generation})
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{Type: v1.PostgreSQLTopologyDegraded, Status: degraded, Reason: degradedReason, ObservedGeneration: instance.Generation})
}

func postgreSQLPodZone(rc *reconcilecontext.InstanceContext, pod *corev1.Pod) string {
	if pod.Spec.NodeName == "" {
		return ""
	}
	node := &corev1.Node{}
	if err := rc.Client().Get(rc.Context(), types.NamespacedName{Name: pod.Spec.NodeName}, node); err != nil {
		return ""
	}
	return node.Labels[corev1.LabelTopologyZone]
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func desiredPostgreSQLReplicas(instance *v1.KDBInstance) int {
	if instance == nil || instance.Spec.InstanceSet.Replicas == nil {
		return 0
	}
	return int(*instance.Spec.InstanceSet.Replicas)
}

func valueOrOne(value *int32) int32 {
	if value == nil || *value < 1 {
		return 1
	}
	return *value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func stringPtr(value string) *string { return &value }
