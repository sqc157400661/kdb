package pg

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	internalmonitoring "github.com/sqc157400661/kdb/internal/monitoring"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/observed"
	internalsecurity "github.com/sqc157400661/kdb/internal/security"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"
)

type InstanceStepManager struct {
	steps.InstanceStepManager
}

// SetMonitor installs the PostgreSQL/kdb-ha scrape contract and the default
// production alerts. Missing Prometheus Operator CRDs remain an optional
// platform capability, matching the existing MySQL behavior.
func (s *InstanceStepManager) SetMonitor() kube.BindFunc {
	return s.StepBinder("SetPostgreSQLMonitor", func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
		instance := rc.GetInstance()
		if !postgreSQLMonitoringEnabled(instance) {
			return flow.Pass()
		}
		for _, obj := range postgreSQLMonitoringObjects(instance) {
			if err := rc.SetOwnerReference(obj); err != nil {
				return flow.Error(err, "set PostgreSQL monitor owner ref err")
			}
			if err := rc.Apply(obj); err != nil {
				if strings.Contains(err.Error(), "no matches for kind") || strings.Contains(err.Error(), "the server could not find the requested resource") {
					return flow.Pass()
				}
				return flow.Error(err, "apply PostgreSQL monitor resource err")
			}
		}
		return flow.Pass()
	})
}

func postgreSQLMonitoringEnabled(instance *v1.KDBInstance) bool {
	return instance != nil &&
		instance.Spec.PostgreSQL != nil &&
		instance.Spec.PostgreSQL.Exporter != nil &&
		instance.Spec.PostgreSQL.Exporter.Enabled
}

func postgreSQLMonitoringObjects(instance *v1.KDBInstance) []*unstructured.Unstructured {
	labels := map[string]interface{}{"app.kubernetes.io/name": instance.Name, "app.kubernetes.io/managed-by": "kdb-operator", "kdb.io/monitoring": "enabled", naming.LabelInstance: instance.Name}
	podMonitor := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "monitoring.coreos.com/v1", "kind": "PodMonitor",
		"metadata": map[string]interface{}{"name": instance.Name + "-postgresql", "namespace": instance.Namespace, "labels": labels},
		"spec": map[string]interface{}{"namespaceSelector": map[string]interface{}{"matchNames": []interface{}{instance.Namespace}}, "selector": map[string]interface{}{"matchLabels": map[string]interface{}{naming.LabelInstance: instance.Name}}, "podMetricsEndpoints": []interface{}{
			map[string]interface{}{"port": naming.PortPostgreSQLHA, "path": "/metrics", "scheme": "https", "interval": "15s", "relabelings": internalmonitoring.PodTargetRelabelings(instance), "tlsConfig": map[string]interface{}{"ca": map[string]interface{}{"secret": map[string]interface{}{"name": naming.PostgreSQLTLSSecret(instance).Name, "key": naming.PostgreSQLTLSCAKey}}, "cert": map[string]interface{}{"secret": map[string]interface{}{"name": naming.PostgreSQLTLSSecret(instance).Name, "key": naming.PostgreSQLTLSClientCertKey}}, "keySecret": map[string]interface{}{"name": naming.PostgreSQLTLSSecret(instance).Name, "key": naming.PostgreSQLTLSClientPrivateKey}, "insecureSkipVerify": true}, "basicAuth": map[string]interface{}{"username": map[string]interface{}{"name": postgreSQLCredentialSecretRef(instance).Name, "key": naming.PostgreSQLRESTAPIUsernameKey}, "password": map[string]interface{}{"name": postgreSQLCredentialSecretRef(instance).Name, "key": naming.PostgreSQLRESTAPIPasswordKey}}},
			map[string]interface{}{"port": naming.PortPostgreSQLMetrics, "path": "/metrics", "interval": "30s", "relabelings": internalmonitoring.PodTargetRelabelings(instance)},
		}},
	}}
	podMonitor.SetGroupVersionKind(schema.FromAPIVersionAndKind("monitoring.coreos.com/v1", "PodMonitor"))
	podMatcher := fmt.Sprintf(`namespace="%s",pod=~"%s.*"`, instance.Namespace, instance.Name)
	rules := internalmonitoring.InstanceResourceRecordingRules(instance)
	rules = append(rules,
		pgAlert("KDBPostgreSQLExporterDown", fmt.Sprintf(`pg_up{%s} == 0`, podMatcher), "2m", "critical", "PostgreSQL exporter cannot reach the database"),
		pgAlert("KDBPostgreSQLSQLProbeDown", fmt.Sprintf(`kdb_pg_probe_sql_up{%s} == 0`, podMatcher), "2m", "critical", "PostgreSQL read-only SQL probe is failing"),
		pgAlert("KDBPostgreSQLHADown", fmt.Sprintf(`up{%s,container="kdb-ha"} == 0`, podMatcher), "2m", "critical", "kdb-ha metrics endpoint is down"),
		pgAlert("KDBPostgreSQLPrimaryMissing", fmt.Sprintf(`sum(kdb_ha_postgres_primary{%s}) < 1`, podMatcher), "2m", "critical", "PostgreSQL primary is missing"),
		pgAlert("KDBPostgreSQLSplitBrainRisk", fmt.Sprintf(`sum(kdb_ha_postgres_primary{%s}) > 1`, podMatcher), "0m", "critical", "More than one PostgreSQL primary is reported"),
		pgAlert("KDBPostgreSQLReplicationLagHigh", fmt.Sprintf(`max(pg_replication_lag_seconds{%s}) > 60`, podMatcher), "5m", "warning", "PostgreSQL replication lag is high"),
		pgAlert("KDBPostgreSQLConnectionsHigh", fmt.Sprintf(`sum(pg_stat_database_numbackends{%s}) / clamp_min(max(kdb_pg_settings_max_connections{%s}), 1) > 0.85`, podMatcher, podMatcher), "10m", "warning", "PostgreSQL connection usage exceeds 85 percent"),
		pgAlert("KDBPostgreSQLLongTransaction", fmt.Sprintf(`max(kdb_pg_activity_longest_transaction_seconds{%s}) > 300`, podMatcher), "5m", "warning", "PostgreSQL transaction has been open for more than five minutes"),
		pgAlert("KDBPostgreSQLDeadlocks", fmt.Sprintf(`sum(increase(pg_stat_database_deadlocks{%s}[5m])) > 0`, podMatcher), "0m", "warning", "PostgreSQL deadlock detected"),
		pgAlert("KDBPostgreSQLFreezeAgeHigh", fmt.Sprintf(`max(kdb_pg_database_freeze_age_ratio{%s}) > 0.8`, podMatcher), "10m", "critical", "PostgreSQL database transaction ID freeze age exceeds 80 percent"),
		pgAlert("KDBPostgreSQLArchiveFailure", fmt.Sprintf(`max(pg_stat_archiver_failed_count{%s}) > 0`, podMatcher), "5m", "critical", "PostgreSQL WAL archive is failing"),
		pgAlert("KDBPostgreSQLBackupFailure", fmt.Sprintf(`max(kdb_pgbackrest_backup_last_success_timestamp_seconds{%s}) < time() - 90000`, podMatcher), "10m", "critical", "PostgreSQL backup is stale or failing"),
		pgAlert("KDBPostgreSQLDiskPressure", fmt.Sprintf(`max(kubelet_volume_stats_used_bytes{namespace="%s",persistentvolumeclaim=~".*%s.*"} / kubelet_volume_stats_capacity_bytes{namespace="%s",persistentvolumeclaim=~".*%s.*"}) > 0.85`, instance.Namespace, instance.Name, instance.Namespace, instance.Name), "10m", "warning", "PostgreSQL volume usage exceeds 85 percent"),
	)
	rule := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "monitoring.coreos.com/v1", "kind": "PrometheusRule", "metadata": map[string]interface{}{"name": instance.Name + "-postgresql", "namespace": instance.Namespace, "labels": labels}, "spec": map[string]interface{}{"groups": []interface{}{map[string]interface{}{"name": "kdb.postgresql." + instance.Name, "rules": rules}}}}}
	rule.SetGroupVersionKind(schema.FromAPIVersionAndKind("monitoring.coreos.com/v1", "PrometheusRule"))
	return []*unstructured.Unstructured{podMonitor, rule}
}

func pgAlert(name, expr, duration, severity, summary string) map[string]interface{} {
	return map[string]interface{}{"alert": name, "expr": expr, "for": duration, "labels": map[string]interface{}{"severity": severity, "engine": "postgresql"}, "annotations": map[string]interface{}{"summary": summary}}
}

const postgreSQLExporterQueries = `kdb_pg_activity:
  query: |
    SELECT COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - min(xact_start))), 0)::float8 AS longest_transaction_seconds,
           count(*) FILTER (WHERE state = 'active')::float8 AS active_sessions,
           count(*) FILTER (WHERE state = 'idle in transaction')::float8 AS idle_in_transaction_sessions
    FROM pg_stat_activity
    WHERE pid <> pg_backend_pid()
  metrics:
    - longest_transaction_seconds:
        usage: GAUGE
        description: Longest open PostgreSQL transaction in seconds.
    - active_sessions:
        usage: GAUGE
        description: Active PostgreSQL sessions.
    - idle_in_transaction_sessions:
        usage: GAUGE
        description: PostgreSQL sessions idle in transaction.
kdb_pg_probe:
  query: |
    SELECT 1::float8 AS sql_up
  metrics:
    - sql_up:
        usage: GAUGE
        description: Bounded read-only PostgreSQL SQL probe.
kdb_pg_replication:
  query: |
    SELECT CASE WHEN pg_is_in_recovery()
                THEN COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - pg_last_xact_replay_timestamp())), 0)
                ELSE 0 END::float8 AS replay_lag_seconds,
           CASE WHEN pg_is_in_recovery() THEN 0
                ELSE COALESCE((SELECT max(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn))
                               FROM pg_replication_slots), 0) END::float8 AS slot_retained_bytes
  metrics:
    - replay_lag_seconds:
        usage: GAUGE
        description: PostgreSQL WAL replay lag in seconds.
    - slot_retained_bytes:
        usage: GAUGE
        description: Maximum WAL bytes retained by a replication slot.
kdb_pg_wal:
  query: |
    SELECT pg_wal_lsn_diff(
             CASE WHEN pg_is_in_recovery() THEN pg_last_wal_replay_lsn() ELSE pg_current_wal_lsn() END,
             '0/0'
           )::float8 AS bytes_total
  metrics:
    - bytes_total:
        usage: COUNTER
        description: PostgreSQL WAL position expressed as cumulative bytes.
kdb_pg_vacuum:
  query: |
    SELECT COALESCE((SELECT sum(n_dead_tup) FROM pg_stat_user_tables), 0)::float8 AS dead_tuples,
           COALESCE((SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'autovacuum worker'), 0)::float8 AS autovacuum_workers
  metrics:
    - dead_tuples:
        usage: GAUGE
        description: Estimated dead tuples across user tables.
    - autovacuum_workers:
        usage: GAUGE
        description: Running autovacuum workers.
kdb_pg_database:
  query: |
    SELECT COALESCE(max(age(datfrozenxid)::float8 / 2000000000.0), 0)::float8 AS freeze_age_ratio
    FROM pg_database
    WHERE datallowconn
  metrics:
    - freeze_age_ratio:
        usage: GAUGE
        description: Maximum database transaction ID age relative to the wraparound limit.
kdb_pg_settings:
  query: |
    SELECT setting::float8 AS max_connections
    FROM pg_settings
    WHERE name = 'max_connections'
  metrics:
    - max_connections:
        usage: GAUGE
        description: PostgreSQL max_connections setting.
kdb_pg_stat_statements:
  query: |
    SELECT COALESCE(sum(calls), 0)::float8 AS calls_total,
           COALESCE(sum(total_exec_time) / 1000.0, 0)::float8 AS exec_seconds_total,
           COALESCE(sum(calls) FILTER (WHERE mean_exec_time >= 1000), 0)::float8 AS slow_calls_total,
           COALESCE(max(mean_exec_time), 0)::float8 AS mean_exec_time_ms
    FROM pg_stat_statements
  metrics:
    - calls_total:
        usage: COUNTER
        description: Aggregated pg_stat_statements calls.
    - exec_seconds_total:
        usage: COUNTER
        description: Aggregated pg_stat_statements execution time in seconds.
    - slow_calls_total:
        usage: COUNTER
        description: Calls whose normalized statement mean execution time is at least one second.
    - mean_exec_time_ms:
        usage: GAUGE
        description: Maximum normalized statement mean execution time in milliseconds.
`

// SetGlobalConfig is optional for standalone PostgreSQL instances because the
// initial PG path can use the images and runtime settings declared directly on
// KDBInstance.spec.instance.
func (s *InstanceStepManager) SetGlobalConfig() kube.BindFunc {
	return s.StepBinder(
		"SetPostgreSQLGlobalConfig",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			return flow.Pass()
		})
}

// SetInstanceConfig renders the minimal Patroni config used by PostgreSQL pods.
func (s *InstanceStepManager) SetInstanceConfig() kube.BindFunc {
	return s.StepBinder(
		"SetPostgreSQLInstanceConfig",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			if err := ensurePostgreSQLCredentialSecret(rc, instance); err != nil {
				return flow.Error(err, "ensure postgresql credential secret err")
			}
			if err := ensurePostgreSQLTLSSecret(rc, instance); err != nil {
				return flow.Error(err, "ensure postgresql TLS secret err")
			}

			instanceConfigMap := &corev1.ConfigMap{ObjectMeta: naming.InstanceConfigMap(instance)}
			instanceConfigMap.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))

			if err := errors.WithStack(rc.SetControllerReference(instanceConfigMap)); err != nil {
				return flow.Error(err, "set configmap reference err")
			}
			instanceConfigMap.Annotations = instance.Annotations
			instanceConfigMap.Labels = naming.Merge(instance.Labels, map[string]string{
				naming.LabelInstance: instance.Name,
			})

			patroni, err := buildPatroniConfig(instance)
			if err != nil {
				return flow.Error(err, "build patroni config err")
			}
			patroniBytes, err := yaml.Marshal(patroni)
			if err != nil {
				return flow.Error(err, "marshal patroni config err")
			}

			util.StringMap(&instanceConfigMap.Data)
			instanceConfigMap.Data[naming.PatroniConfigKey] = naming.YamlGeneratedWarning + string(patroniBytes)
			if pgBackRestEnabled(instance) {
				instanceConfigMap.Data[naming.PGBackRestConfigKey] = naming.YamlGeneratedWarning + buildPGBackRestConfig(instance)
			} else {
				delete(instanceConfigMap.Data, naming.PGBackRestConfigKey)
			}
			if postgreSQLMonitoringEnabled(instance) {
				instanceConfigMap.Data["postgres-exporter-queries.yaml"] = postgreSQLExporterQueries
			} else {
				delete(instanceConfigMap.Data, "postgres-exporter-queries.yaml")
			}

			if err := errors.WithStack(rc.Apply(instanceConfigMap)); err != nil {
				return flow.Error(err, "apply postgresql configmap err")
			}
			rc.SetInstanceConfigMap(instanceConfigMap)
			progressed, err := reconcilePostgreSQLBootstrapParameters(rc, instance)
			if err != nil {
				return flow.Error(err, "apply postgresql bootstrap parameters err")
			}
			if progressed {
				return flow.Retry("postgresql bootstrap parameter merge is running")
			}
			return flow.Pass()
		})
}

func reconcilePostgreSQLBootstrapParameters(rc *context.InstanceContext, instance *v1.KDBInstance) (bool, error) {
	if instance.Spec.PostgreSQL == nil || instance.Status.PostgreSQL == nil || !instance.Status.PostgreSQL.Ready || instance.Status.PostgreSQL.BootstrapParameters != nil {
		return false, nil
	}
	parameters := instance.Spec.PostgreSQL.Parameters
	inputHash := postgreSQLParametersHash(parameters)
	if len(parameters) == 0 {
		instance.Status.PostgreSQL.BootstrapParameters = &v1.PostgreSQLBootstrapParametersStatus{InputHash: inputHash, Revision: instance.Status.PostgreSQL.DynamicConfigRevision, AppliedAt: metav1.Now(), Method: "kdb-hactl-edit-config", State: "Applied"}
		return false, nil
	}
	jobName := instance.Name + "-pg-bootstrap-config"
	if len(jobName) > 63 {
		jobName = strings.TrimRight(jobName[:63], "-")
	}
	job := &batchv1.Job{}
	err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: jobName}, job)
	if err == nil {
		if job.Status.Succeeded > 0 {
			instance.Status.PostgreSQL.BootstrapParameters = &v1.PostgreSQLBootstrapParametersStatus{InputHash: inputHash, Revision: instance.Status.PostgreSQL.DynamicConfigRevision, AppliedAt: metav1.Now(), Method: "kdb-hactl-edit-config", State: "Applied"}
			return false, nil
		}
		if job.Status.Failed > 0 {
			return false, fmt.Errorf("bootstrap config Job %s failed", jobName)
		}
		return true, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, err
	}
	primaryAddress := ""
	for _, endpoint := range instance.Status.PostgreSQL.Endpoints {
		if endpoint.PodName == instance.Status.PostgreSQL.Primary {
			primaryAddress = endpoint.Host
			break
		}
	}
	if primaryAddress == "" {
		return true, nil
	}
	primaryPort := naming.KDBInstanceMasterPort(instance)
	if primaryPort == 0 {
		primaryPort = 5432
	}
	primaryConnectAddress := net.JoinHostPort(primaryAddress, strconv.Itoa(int(primaryPort)))
	endpoint := fmt.Sprintf("https://%s.%s.%s.svc:8008", instance.Status.PostgreSQL.Primary, naming.InstancePodServiceName(instance.Name), instance.Namespace)
	args := []string{"-endpoint", endpoint, "-config", naming.PatroniConfigMountPath + "/" + naming.PatroniConfigKey, "edit-config", "--expected-revision", fmt.Sprint(instance.Status.PostgreSQL.DynamicConfigRevision)}
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--pg", key+"="+parameters[key])
	}
	backoff := int32(0)
	fsGroup := int64(102)
	fsGroupChangePolicy := corev1.FSGroupChangeOnRootMismatch
	job = &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: instance.Namespace, Labels: map[string]string{naming.LabelInstance: instance.Name}}, Spec: batchv1.JobSpec{
		BackoffLimit: &backoff,
		Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{naming.LabelInstance: instance.Name}}, Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{
				FSGroup:             &fsGroup,
				FSGroupChangePolicy: &fsGroupChangePolicy,
			},
			Volumes: []corev1.Volume{
				{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: naming.InstanceConfigMap(instance).Name}}}},
				{Name: "tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: naming.PostgreSQLTLSSecret(instance).Name, DefaultMode: func() *int32 { v := int32(0o440); return &v }()}}},
			},
			Containers: []corev1.Container{{Name: "kdb-hactl", Image: naming.InstanceSetSpec(instance).SidecarContainer.Image, Command: []string{"kdb-hactl"}, Args: args,
				Env: []corev1.EnvVar{
					{Name: "PATRONI_NAME", Value: jobName},
					{Name: "PATRONI_POSTGRESQL_CONNECT_ADDRESS", Value: primaryConnectAddress},
					postgreSQLCredentialEnv(instance, "PATRONI_RESTAPI_USERNAME", naming.PostgreSQLRESTAPIUsernameKey),
					postgreSQLCredentialEnv(instance, "PATRONI_RESTAPI_PASSWORD", naming.PostgreSQLRESTAPIPasswordKey),
				},
				VolumeMounts: []corev1.VolumeMount{{Name: "config", MountPath: naming.PatroniConfigMountPath, ReadOnly: true}, {Name: "tls", MountPath: "/etc/postgresql/tls", ReadOnly: true}},
			}},
		}},
	}}
	if err := rc.SetControllerReference(job); err != nil {
		return false, err
	}
	if err := rc.Client().Create(rc.Context(), job); err != nil {
		return false, err
	}
	return true, nil
}

func postgreSQLCredentialEnv(instance *v1.KDBInstance, name, key string) corev1.EnvVar {
	return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: postgreSQLCredentialSecretRef(instance), Key: key}}}
}

func postgreSQLParametersHash(parameters map[string]string) string {
	data, _ := json.Marshal(parameters)
	hash := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", hash[:])
}

func ensurePostgreSQLCredentialSecret(rc *context.InstanceContext, instance *v1.KDBInstance) error {
	ref := postgreSQLCredentialSecretRef(instance)
	secret := &corev1.Secret{}
	err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: ref.Name}, secret)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return errors.WithStack(err)
	}
	if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.CredentialSecretRef != nil &&
		instance.Spec.PostgreSQL.CredentialSecretRef.Name != "" {
		return errors.Errorf("referenced postgresql credential secret %s/%s not found", instance.Namespace, ref.Name)
	}

	secret = &corev1.Secret{ObjectMeta: naming.PostgreSQLCredentialSecret(instance)}
	secret.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
	secret.Type = corev1.SecretTypeOpaque
	secret.Labels = naming.Merge(instance.Labels, map[string]string{
		naming.LabelInstance: instance.Name,
	})
	secret.Annotations = naming.Merge(instance.Annotations)
	secret.Data = map[string][]byte{
		naming.PostgreSQLSuperuserUsernameKey:   []byte("postgres"),
		naming.PostgreSQLSuperuserPasswordKey:   []byte(randomHex(24)),
		naming.PostgreSQLReplicationUsernameKey: []byte("replicator"),
		naming.PostgreSQLReplicationPasswordKey: []byte(randomHex(24)),
		naming.PostgreSQLBackupUsernameKey:      []byte("kdb_backup"),
		naming.PostgreSQLBackupPasswordKey:      []byte(randomHex(24)),
		naming.PostgreSQLMonitoringUsernameKey:  []byte("kdb_monitor"),
		naming.PostgreSQLMonitoringPasswordKey:  []byte(randomHex(24)),
		naming.PostgreSQLRESTAPIUsernameKey:     []byte("kdb_control_plane"),
		naming.PostgreSQLRESTAPIPasswordKey:     []byte(randomHex(24)),
	}
	if err := errors.WithStack(rc.SetControllerReference(secret)); err != nil {
		return err
	}
	return errors.WithStack(rc.Client().Create(rc.Context(), secret))
}

func ensurePostgreSQLTLSSecret(rc *context.InstanceContext, instance *v1.KDBInstance) error {
	meta := naming.PostgreSQLTLSSecret(instance)
	secret := &corev1.Secret{}
	err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: meta.Namespace, Name: meta.Name}, secret)
	exists := err == nil
	if err == nil {
		if len(secret.Data[naming.PostgreSQLTLSCAKey]) > 0 &&
			len(secret.Data[naming.PostgreSQLTLSCertKey]) > 0 &&
			len(secret.Data[naming.PostgreSQLTLSPrivateKey]) > 0 &&
			len(secret.Data[naming.PostgreSQLTLSClientCertKey]) > 0 &&
			len(secret.Data[naming.PostgreSQLTLSClientPrivateKey]) > 0 {
			return nil
		}
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return errors.WithStack(err)
	}
	bundle, err := generatePostgreSQLMTLSBundle(instance)
	if err != nil {
		return err
	}
	if !exists {
		secret = &corev1.Secret{ObjectMeta: meta, Type: corev1.SecretTypeTLS}
	}
	secret.Data = map[string][]byte{
		naming.PostgreSQLTLSCAKey: bundle.CA, naming.PostgreSQLTLSCertKey: bundle.ServerCert,
		naming.PostgreSQLTLSPrivateKey: bundle.ServerKey, naming.PostgreSQLTLSClientCertKey: bundle.ClientCert,
		naming.PostgreSQLTLSClientPrivateKey: bundle.ClientKey,
	}
	secret.Labels = naming.Merge(instance.Labels, map[string]string{naming.LabelInstance: instance.Name})
	if err := rc.SetControllerReference(secret); err != nil {
		return err
	}
	if exists {
		return errors.WithStack(rc.Client().Update(rc.Context(), secret))
	}
	return errors.WithStack(rc.Client().Create(rc.Context(), secret))
}

func generatePostgreSQLTLSBundle(instance *v1.KDBInstance) ([]byte, []byte, []byte, error) {
	bundle, err := generatePostgreSQLMTLSBundle(instance)
	return bundle.CA, bundle.ServerCert, bundle.ServerKey, err
}

func generatePostgreSQLMTLSBundle(instance *v1.KDBInstance) (internalsecurity.RuntimeMTLSBundle, error) {
	rw := naming.InstanceReadWriteServiceName(instance.Name)
	return internalsecurity.GenerateRuntimeMTLSBundleWithDNS(instance, "PostgreSQL", []string{
		rw, rw + "." + instance.Namespace, rw + "." + instance.Namespace + ".svc",
	})
}

func postgreSQLCredentialSecretRef(instance *v1.KDBInstance) corev1.LocalObjectReference {
	if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.CredentialSecretRef != nil &&
		instance.Spec.PostgreSQL.CredentialSecretRef.Name != "" {
		return *instance.Spec.PostgreSQL.CredentialSecretRef
	}
	return corev1.LocalObjectReference{Name: naming.PostgreSQLCredentialSecret(instance).Name}
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("kdb-postgresql-%d", size)
	}
	return hex.EncodeToString(buf)
}

// SetService creates DNS, read-write and read-only Services for PostgreSQL.
func (s *InstanceStepManager) SetService() kube.BindFunc {
	return s.StepBinder(
		"SetPostgreSQLService",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			port := naming.KDBInstanceMasterPort(instance)
			if port == 0 {
				port = 5432
			}

			headless := newPostgreSQLService(instance, naming.InstancePodServiceName(instance.Name), corev1.ClusterIPNone, map[string]string{
				naming.LabelInstance: instance.Name,
			}, port)
			rw := newPostgreSQLService(instance, naming.InstanceReadWriteServiceName(instance.Name), "", map[string]string{
				naming.LabelInstance: instance.Name,
				naming.LabelRole:     naming.MasterRole,
			}, port)
			ro := newPostgreSQLService(instance, naming.InstanceReadOnlyServiceName(instance.Name), "", nil, port)
			any := newPostgreSQLService(instance, naming.PostgreSQLAnyServiceName(instance.Name), "", nil, port)
			rw.Spec.Selector = nil
			rw.Spec.Ports = append(rw.Spec.Ports, corev1.ServicePort{Name: naming.PortPostgreSQLHA, Port: 8008})

			for _, service := range []*corev1.Service{headless, rw, ro, any} {
				service.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
				if err := errors.WithStack(rc.SetControllerReference(service)); err != nil {
					return flow.Error(err, "set postgresql service controller ref err")
				}
				if err := errors.WithStack(rc.Apply(service)); err != nil {
					return flow.Error(err, "apply postgresql service err")
				}
			}
			policy := postgreSQLNetworkPolicy(instance, port)
			if err := errors.WithStack(rc.SetControllerReference(policy)); err != nil {
				return flow.Error(err, "set postgresql NetworkPolicy controller ref err")
			}
			if err := errors.WithStack(rc.Apply(policy)); err != nil {
				return flow.Error(err, "apply postgresql NetworkPolicy err")
			}
			if err := reconcilePostgreSQLPgBouncer(rc, instance); err != nil {
				return flow.Error(err, "apply PostgreSQL PgBouncer err")
			}
			if err := reconcilePostgreSQLBackupSchedules(rc, instance); err != nil {
				return flow.Error(err, "apply PostgreSQL backup schedules err")
			}
			rc.SetInstancePodService(headless)
			return flow.Pass()
		})
}

func postgreSQLNetworkPolicy(instance *v1.KDBInstance, port int32) *networkingv1.NetworkPolicy {
	tcp := corev1.ProtocolTCP
	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: instance.Name + "-postgresql-default-deny", Namespace: instance.Namespace, Labels: map[string]string{naming.LabelInstance: instance.Name}}, Spec: networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{naming.LabelInstance: instance.Name}},
		PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		Ingress: []networkingv1.NetworkPolicyIngressRule{
			{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{naming.LabelInstance: instance.Name}}}}},
			{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kdb.io/postgresql-client": "allowed"}}}}, Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: postgreSQLPolicyPort(int(port))}, {Protocol: &tcp, Port: postgreSQLPolicyPort(8008)}, {Protocol: &tcp, Port: postgreSQLPolicyPort(9187)}}},
			{From: []networkingv1.NetworkPolicyPeer{
				{PodSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app.kubernetes.io/name", Operator: metav1.LabelSelectorOpIn, Values: []string{"kdb-operator", "kdb-cluster-gateway"}}}}},
				// Keep accepting the legacy label while older deployments are
				// upgraded to the canonical app.kubernetes.io/name contract.
				{PodSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: metav1.LabelSelectorOpIn, Values: []string{"kdb-operator", "kdb-cluster-gateway"}}}}},
			}, Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: postgreSQLPolicyPort(int(port))}, {Protocol: &tcp, Port: postgreSQLPolicyPort(8008)}}},
			{From: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kdb-observability"}}}}, Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: postgreSQLPolicyPort(8008)}, {Protocol: &tcp, Port: postgreSQLPolicyPort(9187)}}},
		},
	}}
	// A DR standby connects from another Kubernetes cluster, so its source Pod
	// labels are not visible to this cluster's NetworkPolicy engine. PostgreSQL
	// still requires CA-verified client certificates and the replication role's
	// SCRAM credential, while this rule makes the database port routable across
	// the cluster boundary. Platform networking may narrow the routable source
	// CIDRs outside this namespace-level contract.
	if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.DR != nil && instance.Spec.PostgreSQL.DR.Enabled {
		policy.Spec.Ingress = append(policy.Spec.Ingress, networkingv1.NetworkPolicyIngressRule{
			From:  []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0"}}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: postgreSQLPolicyPort(int(port))}},
		})
	}
	policy.SetGroupVersionKind(networkingv1.SchemeGroupVersion.WithKind("NetworkPolicy"))
	return policy
}

func postgreSQLPolicyPort(value int) *intstr.IntOrString { port := intstr.FromInt(value); return &port }

func reconcilePostgreSQLBackupSchedules(rc *context.InstanceContext, instance *v1.KDBInstance) error {
	if !pgBackRestEnabled(instance) || instance.Status.PostgreSQL == nil || !instance.Status.PostgreSQL.Ready {
		return nil
	}
	now := time.Now().UTC().Truncate(time.Minute)
	schedules := []struct {
		name       string
		expression string
		backupType string
		validate   bool
	}{
		{name: "full", expression: pgBackRestFullSchedule(instance), backupType: "full"},
		{name: "diff", expression: pgBackRestDifferentialSchedule(instance), backupType: "diff"},
		{name: "validation", expression: pgBackRestValidationSchedule(instance), backupType: "full", validate: true},
	}
	for _, schedule := range schedules {
		matches, err := postgreSQLCronMatches(schedule.expression, now)
		if err != nil {
			return fmt.Errorf("parse PostgreSQL %s backup schedule %q: %w", schedule.name, schedule.expression, err)
		}
		if !matches {
			continue
		}
		if err := createScheduledPostgreSQLBackup(rc, instance, schedule.name, schedule.backupType, schedule.validate, now); err != nil {
			return err
		}
	}
	return nil
}

func postgreSQLCronMatches(expression string, now time.Time) (bool, error) {
	schedule, err := cron.ParseStandard("CRON_TZ=UTC " + strings.TrimSpace(expression))
	if err != nil {
		return false, err
	}
	return schedule.Next(now.Add(-time.Minute)).Equal(now), nil
}

func createScheduledPostgreSQLBackup(rc *context.InstanceContext, instance *v1.KDBInstance, scheduleName, backupType string, validate bool, now time.Time) error {
	operationID := fmt.Sprintf("scheduled-%s-%s", scheduleName, now.Format("20060102-1504"))
	suffix := "-" + operationID
	name := instance.Name
	if len(name)+len(suffix) > 63 {
		name = strings.TrimRight(name[:63-len(suffix)], "-")
	}
	name += suffix
	existing := &v1.DBBackup{}
	err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: name}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	backup := &v1.DBBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: instance.Namespace,
			Labels: map[string]string{
				naming.LabelInstance:      instance.Name,
				"kdb.com/backup-type":     backupType,
				"kdb.com/backup-schedule": scheduleName,
				"kdb.com/schedule-minute": now.Format("2006-01-02T15-04Z"),
			},
		},
		Spec: v1.DBBackupSpec{
			OperationID: operationID,
			InstanceRef: corev1.LocalObjectReference{Name: instance.Name},
			Type:        backupType,
			Validate:    validate,
			Reason:      fmt.Sprintf("operator managed PostgreSQL %s schedule", scheduleName),
		},
	}
	backup.SetGroupVersionKind(v1.GroupVersion.WithKind("DBBackup"))
	if err := rc.SetControllerReference(backup); err != nil {
		return err
	}
	if err := rc.Client().Create(rc.Context(), backup); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func newPostgreSQLService(instance *v1.KDBInstance, name, clusterIP string, selector map[string]string, port int32) *corev1.Service {
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Namespace: instance.Namespace,
		Name:      name,
	}}
	service.Annotations = instance.Annotations
	service.Labels = naming.Merge(instance.Labels, map[string]string{
		naming.LabelInstance: instance.Name,
	})
	service.Spec = corev1.ServiceSpec{
		ClusterIP: clusterIP,
		Selector:  selector,
		Ports: []corev1.ServicePort{{
			Name: naming.PortDatabase,
			Port: port,
		}},
	}
	return service
}

// InitObservedRunner projects Kubernetes resources into generic and PostgreSQL status.
func (s *InstanceStepManager) InitObservedRunner() kube.BindFunc {
	return s.StepBinder(
		"InitObservedPostgreSQLInstances",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			progressed, err := reconcilePostgreSQLExternalFence(rc, instance)
			if err != nil {
				return flow.Error(err, "reconcile postgresql external fence err")
			}
			if progressed {
				return flow.Retry("postgresql external fence progressed")
			}
			pods := &corev1.PodList{}
			runners := &appsv1.StatefulSetList{}
			selector, err := naming.AsSelector(naming.KDBInstance(rc.Name()))
			if err != nil {
				return flow.Error(err, "get selector err")
			}
			if err = rc.List(pods, selector); err != nil {
				return flow.Error(err, "get pod list err")
			}
			if err = rc.List(runners, selector); err != nil {
				return flow.Error(err, "get runners list err")
			}

			obs := observed.NewObservedRunner(instance, runners.Items, pods.Items)
			rc.SetObservedRunner(obs)
			instance.Status.InstanceSet = shared.InstanceSetStatus{Replicas: *instance.Spec.InstanceSet.Replicas}
			pgStatus, validatedRoles, err := observePostgreSQLRuntime(rc, instance, pods.Items)
			if err != nil {
				return flow.Error(err, "observe PostgreSQL Lease/runtime status err")
			}
			if err := reconcilePostgreSQLPodRoles(rc, pods.Items, validatedRoles); err != nil {
				return flow.Error(err, "project PostgreSQL Pod roles err")
			}
			port := naming.KDBInstanceMasterPort(instance)
			if port == 0 {
				port = 5432
			}

			for _, item := range obs.List {
				if item == nil || len(item.Pods) == 0 {
					continue
				}
				pod := item.Pods[0]
				if util.IsPodReady(pod) {
					instance.Status.InstanceSet.ReadyReplicas++
					instance.Status.InstanceSet.PodInfos = append(instance.Status.InstanceSet.PodInfos, shared.PodStatusInfo{
						PodName:  pod.Name,
						PodUID:   string(pod.UID),
						PodPhase: pod.Status.Phase,
						PodIP:    pod.Status.PodIP,
						NodeName: pod.Spec.NodeName,
						HostIP:   pod.Status.HostIP,
					})
					if pod.Status.PodIP != "" {
						pgStatus.Endpoints = append(pgStatus.Endpoints, v1.HostInfo{
							PodName: pod.Name,
							Host:    pod.Status.PodIP,
							Port:    port,
						})
					}
				}
				if matches, known := item.PodMatchesPodTemplate(); known && matches {
					instance.Status.InstanceSet.UpdatedReplicas++
				}
			}
			pgStatus.Ready = instance.Status.InstanceSet.ReadyReplicas == instance.Status.InstanceSet.Replicas &&
				(pgStatus.Primary != "" || pgStatus.DR != nil && pgStatus.DR.RuntimeRole == "standby")
			if err := reconcilePostgreSQLEndpointSlices(rc, instance, pgStatus, pods.Items, validatedRoles, port); err != nil {
				return flow.Error(err, "reconcile PostgreSQL EndpointSlices err")
			}
			pgStatus.PgBouncer = observePostgreSQLPgBouncer(rc, instance)
			if pgBackRestEnabled(instance) {
				observedBackup := &v1.PostgreSQLPGBackRestStatus{
					Enabled:              true,
					Stanza:               pgBackRestStanza(instance),
					RepoType:             pgBackRestRepoType(instance),
					ConfigMapName:        naming.InstanceConfigMap(instance).Name,
					RetentionDays:        pgBackRestRetentionDays(instance),
					FullSchedule:         pgBackRestFullSchedule(instance),
					DifferentialSchedule: pgBackRestDifferentialSchedule(instance),
					WALArchiveEnabled:    true,
				}
				if previous := instance.Status.PostgreSQL; previous != nil && previous.PGBackRest != nil {
					observedBackup.LatestBackupRef = previous.PGBackRest.LatestBackupRef
					observedBackup.LastSuccessfulBackupTime = previous.PGBackRest.LastSuccessfulBackupTime
					observedBackup.LastRestoreValidationTime = previous.PGBackRest.LastRestoreValidationTime
					observedBackup.LastRestoreValidationStatus = previous.PGBackRest.LastRestoreValidationStatus
					observedBackup.PITRWindowStart = previous.PGBackRest.PITRWindowStart
					observedBackup.PITRWindowEnd = previous.PGBackRest.PITRWindowEnd
				}
				pgStatus.PGBackRest = observedBackup
			}
			instance.Status.PostgreSQL = pgStatus
			switch {
			case pgStatus.Ready:
				instance.Status.Phase = "Running"
			case instance.Status.InstanceSet.Replicas > 0 &&
				instance.Status.InstanceSet.ReadyReplicas == instance.Status.InstanceSet.Replicas:
				instance.Status.Phase = "Initializing"
			default:
				instance.Status.Phase = "Creating"
			}
			return flow.Pass()
		})
}

func buildPatroniConfig(instance *v1.KDBInstance) (map[string]interface{}, error) {
	if instance == nil {
		return nil, fmt.Errorf("nil KDBInstance")
	}
	port := naming.KDBInstanceMasterPort(instance)
	if port == 0 {
		port = 5432
	}
	engineVersion := instance.Spec.EngineVersion
	if engineVersion == "" {
		engineVersion = "14"
	}

	dcs := "kubernetes"
	ttl := int32(30)
	loopWait := int32(10)
	if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Patroni != nil {
		patroni := instance.Spec.PostgreSQL.Patroni
		if patroni.DCS != "" {
			dcs = patroni.DCS
		}
		if patroni.LeaderLeaseDurationSeconds != nil {
			ttl = *patroni.LeaderLeaseDurationSeconds
		}
		if patroni.SyncPeriodSeconds != nil {
			loopWait = *patroni.SyncPeriodSeconds
		}
	}
	if dcs != "kubernetes" {
		return nil, fmt.Errorf("postgresql patroni dcs %q is not implemented yet", dcs)
	}
	haConfig := effectivePostgreSQLHAConfig(instance)
	var drConfig map[string]interface{}
	if pg := instance.Spec.PostgreSQL; pg != nil && pg.DR != nil && pg.DR.Enabled {
		dr := pg.DR
		prefix := strings.TrimSpace(dr.Etcd3.Prefix)
		if prefix == "" {
			prefix = "/kdb/dr"
		}
		drConfig = map[string]interface{}{
			"enabled":               true,
			"cluster_id":            dr.ClusterID,
			"peer_cluster_id":       dr.PeerClusterID,
			"scope":                 dr.Scope,
			"role":                  dr.Role,
			"manual_promotion_only": dr.ManualPromotionOnly,
			"dcs": map[string]interface{}{
				"type":      "etcd3",
				"endpoints": dr.Etcd3.Endpoints,
				"prefix":    prefix,
				"tls": map[string]interface{}{
					"cafile":   "/etc/postgresql/dr-etcd3/ca.crt",
					"certfile": "/etc/postgresql/dr-etcd3/tls.crt",
					"keyfile":  "/etc/postgresql/dr-etcd3/tls.key",
				},
			},
			"wal_source": map[string]interface{}{
				"primary_conninfo": dr.WALSource.PrimaryConnInfo,
				"archive_prefix":   dr.WALSource.ArchivePrefix,
			},
		}
		if dr.Role == "standby" {
			haConfig["standby_cluster"] = map[string]interface{}{
				"primary_conninfo": dr.WALSource.PrimaryConnInfo,
				"archive_prefix":   dr.WALSource.ArchivePrefix,
			}
		}
	}

	parameters := map[string]string{
		"hot_standby":              "on",
		"max_replication_slots":    "10",
		"max_wal_senders":          "10",
		"shared_preload_libraries": "pg_stat_statements",
		"wal_log_hints":            "on",
		"wal_level":                "replica",
		"password_encryption":      "scram-sha-256",
		"ssl":                      "on",
		"ssl_ca_file":              "/etc/postgresql/tls/ca.crt",
		"ssl_cert_file":            "/etc/postgresql/tls/tls.crt",
		"ssl_key_file":             "/etc/postgresql/tls/tls.key",
	}
	if pgBackRestEnabled(instance) {
		configPath := fmt.Sprintf("%s/%s", naming.PGBackRestConfigMountPath, naming.PGBackRestConfigKey)
		stanza := pgBackRestStanza(instance)
		parameters["archive_mode"] = "on"
		// The first WAL switch can race the HA loop that initializes the S3
		// repository. Creating the stanza in the same archive_command process
		// guarantees ordering and remains idempotent for subsequent segments.
		if naming.PostgreSQLSplitRuntime(instance) {
			parameters["archive_command"] = fmt.Sprintf("[ -f /var/run/kdb-ha/pgbackrest-stanza.ready ] && kdb-pg-tool --socket=/var/run/kdb-ha/postgres-tools.sock pgbackrest --config=%s --stanza=%s archive-push %%p", configPath, stanza)
			parameters["restore_command"] = fmt.Sprintf("kdb-pg-tool --socket=/var/run/kdb-ha/postgres-tools.sock pgbackrest --config=%s --stanza=%s archive-get %%f %%p", configPath, stanza)
		} else {
			parameters["archive_command"] = fmt.Sprintf("[ -f /var/run/kdb-ha/pgbackrest-stanza.ready ] && pgbackrest --config=%s --stanza=%s archive-push %%p", configPath, stanza)
			parameters["restore_command"] = fmt.Sprintf("pgbackrest --config=%s --stanza=%s archive-get %%f %%p", configPath, stanza)
		}
	}
	if instance.Spec.PostgreSQL != nil && (instance.Status.PostgreSQL == nil || instance.Status.PostgreSQL.BootstrapParameters == nil) {
		for key, value := range instance.Spec.PostgreSQL.Parameters {
			if key == "archive_command" || key == "data_directory" || key == "hba_file" || key == "port" || key == "restore_command" {
				continue
			}
			parameters[key] = value
		}
	}
	// Canonical file logging is an operator invariant. User parameters must not
	// redirect logs away from the data-PVC-backed inventory root.
	parameters["logging_collector"] = "on"
	parameters["log_directory"] = naming.DatabaseLogRoot
	parameters["log_filename"] = "postgresql-%Y-%m-%d_%H%M%S.log"

	hba := []string{
		"local all postgres peer",
		"local all all scram-sha-256",
		"hostssl replication replicator 0.0.0.0/0 scram-sha-256 clientcert=verify-ca",
		"hostssl all all 0.0.0.0/0 scram-sha-256 clientcert=verify-ca",
		"hostnossl all all 0.0.0.0/0 reject",
	}
	if instance.Spec.PostgreSQL != nil {
		hba = append(hba, instance.Spec.PostgreSQL.HBA...)
	}

	kubernetesConfig := map[string]interface{}{
		"namespace":   instance.Namespace,
		"scope_label": naming.LabelInstance,
		"role_label":  naming.LabelRole,
		"labels": map[string]string{
			naming.LabelInstance: instance.Name,
		},
	}
	localParameters := make(map[string]string, len(parameters)+3)
	for key, value := range parameters {
		localParameters[key] = value
	}
	localParameters["listen_addresses"] = "0.0.0.0"
	localParameters["port"] = fmt.Sprint(port)
	localParameters["unix_socket_directories"] = naming.PostgreSQLSocketDirectory
	result := map[string]interface{}{
		"scope":     instance.Name,
		"namespace": instance.Namespace,
		"name":      instance.Name,
		"restapi": map[string]interface{}{
			"listen": "0.0.0.0:8008", "certfile": "/etc/postgresql/tls/tls.crt", "keyfile": "/etc/postgresql/tls/tls.key", "cafile": "/etc/postgresql/tls/ca.crt", "verify_client": "required",
		},
		// dcs is the native kdb-ha contract. The top-level kubernetes block is
		// retained during migration for Patroni-compatible config readers.
		"dcs":        map[string]interface{}{"type": "kubernetes", "kubernetes": kubernetesConfig},
		"kubernetes": kubernetesConfig,
		"ha":         haConfig,
		"bootstrap": map[string]interface{}{
			"dcs": map[string]interface{}{
				"ttl":                     ttl,
				"loop_wait":               loopWait,
				"maximum_lag_on_failover": haConfig["maximum_lag_on_failover"],
				"maximum_lag_on_syncnode": haConfig["maximum_lag_on_syncnode"],
				"synchronous_mode":        haConfig["synchronous_mode"],
				"synchronous_mode_strict": haConfig["synchronous_mode_strict"],
				"synchronous_node_count":  haConfig["synchronous_node_count"],
				"postgresql": map[string]interface{}{
					"use_slots":  true,
					"parameters": parameters,
					"pg_hba":     hba,
				},
			},
		},
		"postgresql": map[string]interface{}{
			"listen":                 fmt.Sprintf("0.0.0.0:%d", port),
			"data_dir":               fmt.Sprintf("%s/pg%s", naming.PostgreSQLDataMountPath, engineVersion),
			"bin_dir":                fmt.Sprintf("/usr/lib/postgresql/%s/bin", engineVersion),
			"create_replica_methods": []string{"basebackup"},
			"use_pg_rewind":          true,
			"parameters":             localParameters,
			"pg_hba":                 hba,
		},
	}
	if naming.PostgreSQLSplitRuntime(instance) {
		postgresql := result["postgresql"].(map[string]interface{})
		postgresql["runtime_socket"] = "/var/run/kdb-ha/postgres-runtime.sock"
		postgresql["tool_socket"] = "/var/run/kdb-ha/postgres-tools.sock"
	}
	if drConfig != nil {
		result["dr"] = drConfig
	}
	return result, nil
}

func effectivePostgreSQLHAConfig(instance *v1.KDBInstance) map[string]interface{} {
	profile := "single"
	maximumLagOnFailover := int64(16 * 1024 * 1024)
	maximumLagOnSyncNode := int64(8 * 1024 * 1024)
	synchronousMode := false
	synchronousModeStrict := false
	synchronousNodeCount := int32(1)
	if instance != nil && instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.HA != nil {
		ha := instance.Spec.PostgreSQL.HA
		if ha.Profile != "" {
			profile = ha.Profile
		}
		if ha.MaximumLagOnFailoverBytes != nil {
			maximumLagOnFailover = *ha.MaximumLagOnFailoverBytes
		}
		if ha.MaximumLagOnSyncNodeBytes != nil {
			maximumLagOnSyncNode = *ha.MaximumLagOnSyncNodeBytes
		}
		synchronousMode = ha.SynchronousMode
		synchronousModeStrict = ha.SynchronousModeStrict
		if ha.SynchronousNodeCount != nil {
			synchronousNodeCount = *ha.SynchronousNodeCount
		}
	}
	if profile == "strong-ha" {
		synchronousMode = true
		synchronousModeStrict = true
	}
	return map[string]interface{}{
		"maximum_lag_on_failover": maximumLagOnFailover,
		"maximum_lag_on_syncnode": maximumLagOnSyncNode,
		"synchronous_mode":        synchronousMode,
		"synchronous_mode_strict": synchronousModeStrict,
		"synchronous_node_count":  synchronousNodeCount,
	}
}

func pgBackRestEnabled(instance *v1.KDBInstance) bool {
	return instance != nil &&
		instance.Spec.PostgreSQL != nil &&
		instance.Spec.PostgreSQL.Backups != nil &&
		instance.Spec.PostgreSQL.Backups.PGBackRest != nil &&
		instance.Spec.PostgreSQL.Backups.PGBackRest.Enabled
}

func pgBackRestStanza(instance *v1.KDBInstance) string {
	if instance != nil && instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Backups != nil &&
		instance.Spec.PostgreSQL.Backups.PGBackRest != nil && instance.Spec.PostgreSQL.Backups.PGBackRest.Stanza != "" {
		return instance.Spec.PostgreSQL.Backups.PGBackRest.Stanza
	}
	return "db"
}

func pgBackRestRepoType(instance *v1.KDBInstance) string {
	if instance != nil && instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Backups != nil &&
		instance.Spec.PostgreSQL.Backups.PGBackRest != nil && instance.Spec.PostgreSQL.Backups.PGBackRest.RepoType != "" {
		return instance.Spec.PostgreSQL.Backups.PGBackRest.RepoType
	}
	return "local"
}

func pgBackRestRepoPath(instance *v1.KDBInstance) string {
	if instance != nil && instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Backups != nil &&
		instance.Spec.PostgreSQL.Backups.PGBackRest != nil && instance.Spec.PostgreSQL.Backups.PGBackRest.RepoPath != "" {
		return instance.Spec.PostgreSQL.Backups.PGBackRest.RepoPath
	}
	return "/backrestrepo"
}

func buildPGBackRestConfig(instance *v1.KDBInstance) string {
	lines := []string{
		"[global]",
		"process-max=2",
		fmt.Sprintf("repo1-type=%s", pgBackRestNativeRepoType(instance)),
		fmt.Sprintf("repo1-path=%s", pgBackRestRepoPath(instance)),
		"log-level-console=info",
		"log-path=/var/log/pgbackrest",
		"archive-async=y",
		"spool-path=/var/run/kdb-ha/pgbackrest-spool",
	}
	if instance != nil && instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Backups != nil &&
		instance.Spec.PostgreSQL.Backups.PGBackRest != nil && instance.Spec.PostgreSQL.Backups.PGBackRest.RetentionFull != nil {
		lines = append(lines, fmt.Sprintf("repo1-retention-full=%d", *instance.Spec.PostgreSQL.Backups.PGBackRest.RetentionFull))
	} else {
		lines = append(lines, "repo1-retention-full=3")
	}
	lines = append(lines, "repo1-retention-archive-type=full")
	if pgBackRestRepoType(instance) == "s3" && instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Backups != nil && instance.Spec.PostgreSQL.Backups.PGBackRest != nil {
		spec := instance.Spec.PostgreSQL.Backups.PGBackRest
		endpoint, port := pgBackRestS3Endpoint(spec.S3Endpoint)
		lines = append(lines,
			"repo1-cipher-type=aes-256-cbc",
			fmt.Sprintf("repo1-s3-bucket=%s", spec.S3Bucket),
			fmt.Sprintf("repo1-s3-endpoint=%s", endpoint),
			fmt.Sprintf("repo1-s3-region=%s", valueOrDefault(spec.S3Region, "us-east-1")),
			fmt.Sprintf("repo1-s3-uri-style=%s", valueOrDefault(spec.S3URIStyle, "path")),
			fmt.Sprintf("repo1-storage-verify-tls=%s", yesNo(pgBackRestTLSVerify(instance))),
		)
		if port != "" {
			lines = append(lines, fmt.Sprintf("repo1-storage-port=%s", port))
		}
	}
	lines = append(lines,
		"",
		fmt.Sprintf("[%s]", pgBackRestStanza(instance)),
		fmt.Sprintf("pg1-path=%s/pg%s", naming.PostgreSQLDataMountPath, postgreSQLEngineVersion(instance)),
		fmt.Sprintf("pg1-socket-path=%s", naming.PostgreSQLSocketDirectory),
	)
	return strings.Join(lines, "\n") + "\n"
}

func pgBackRestNativeRepoType(instance *v1.KDBInstance) string {
	if pgBackRestRepoType(instance) == "local" {
		return "posix"
	}
	return pgBackRestRepoType(instance)
}

func pgBackRestS3Endpoint(endpoint string) (string, string) {
	value := strings.TrimSpace(endpoint)
	if value == "" {
		return value, ""
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), ""
	}
	return parsed.Hostname(), parsed.Port()
}

func pgBackRestRetentionDays(instance *v1.KDBInstance) int32 {
	if spec := pgBackRestSpec(instance); spec != nil && spec.RetentionDays != nil {
		return *spec.RetentionDays
	}
	return 14
}
func pgBackRestFullSchedule(instance *v1.KDBInstance) string {
	if spec := pgBackRestSpec(instance); spec != nil && spec.FullSchedule != "" {
		return spec.FullSchedule
	}
	return "0 1 * * 0"
}
func pgBackRestDifferentialSchedule(instance *v1.KDBInstance) string {
	if spec := pgBackRestSpec(instance); spec != nil && spec.DifferentialSchedule != "" {
		return spec.DifferentialSchedule
	}
	return "0 1 * * 1-6"
}
func pgBackRestValidationSchedule(instance *v1.KDBInstance) string {
	if spec := pgBackRestSpec(instance); spec != nil && spec.ValidationSchedule != "" {
		return spec.ValidationSchedule
	}
	return "0 3 * * 0"
}
func pgBackRestTLSVerify(instance *v1.KDBInstance) bool {
	if spec := pgBackRestSpec(instance); spec != nil && spec.S3TLSVerify != nil {
		return *spec.S3TLSVerify
	}
	return true
}
func pgBackRestSpec(instance *v1.KDBInstance) *v1.PostgreSQLPGBackRestSpec {
	if instance != nil && instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Backups != nil {
		return instance.Spec.PostgreSQL.Backups.PGBackRest
	}
	return nil
}
func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
func yesNo(value bool) string {
	if value {
		return "y"
	}
	return "n"
}

func postgreSQLEngineVersion(instance *v1.KDBInstance) string {
	if instance != nil && instance.Spec.EngineVersion != "" {
		return instance.Spec.EngineVersion
	}
	return "14"
}
