package pg

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/observed"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"
)

type InstanceStepManager struct {
	steps.InstanceStepManager
}

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

			if err := errors.WithStack(rc.Apply(instanceConfigMap)); err != nil {
				return flow.Error(err, "apply postgresql configmap err")
			}
			rc.SetInstanceConfigMap(instanceConfigMap)
			return flow.Pass()
		})
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
	}
	if err := errors.WithStack(rc.SetControllerReference(secret)); err != nil {
		return err
	}
	return errors.WithStack(rc.Client().Create(rc.Context(), secret))
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
			ro := newPostgreSQLService(instance, naming.InstanceReadOnlyServiceName(instance.Name), "", map[string]string{
				naming.LabelInstance: instance.Name,
				naming.LabelRole:     naming.ReplicaRole,
			}, port)

			for _, service := range []*corev1.Service{headless, rw, ro} {
				service.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
				if err := errors.WithStack(rc.SetControllerReference(service)); err != nil {
					return flow.Error(err, "set postgresql service controller ref err")
				}
				if err := errors.WithStack(rc.Apply(service)); err != nil {
					return flow.Error(err, "apply postgresql service err")
				}
			}
			rc.SetInstancePodService(headless)
			return flow.Pass()
		})
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
			pgStatus := &v1.PostgreSQLStatus{}
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
				switch pod.Labels[naming.LabelRole] {
				case naming.MasterRole:
					pgStatus.Primary = pod.Name
				case naming.ReplicaRole:
					pgStatus.Replicas = append(pgStatus.Replicas, pod.Name)
				}
			}
			pgStatus.Ready = pgStatus.Primary != "" &&
				instance.Status.InstanceSet.ReadyReplicas == instance.Status.InstanceSet.Replicas
			instance.Status.PostgreSQL = pgStatus
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

	parameters := map[string]string{
		"archive_mode":             "on",
		"archive_command":          "pgbackrest --stanza=db archive-push %p",
		"hot_standby":              "on",
		"max_replication_slots":    "10",
		"max_wal_senders":          "10",
		"shared_preload_libraries": "pg_stat_statements",
		"wal_level":                "replica",
	}
	if instance.Spec.PostgreSQL != nil {
		for key, value := range instance.Spec.PostgreSQL.Parameters {
			if key == "data_directory" || key == "hba_file" || key == "port" {
				continue
			}
			parameters[key] = value
		}
	}

	hba := []string{
		"local all all trust",
		"host all all 0.0.0.0/0 md5",
		"host replication all 0.0.0.0/0 md5",
	}
	if instance.Spec.PostgreSQL != nil {
		hba = append(hba, instance.Spec.PostgreSQL.HBA...)
	}

	return map[string]interface{}{
		"scope":     instance.Name,
		"namespace": instance.Namespace,
		"name":      instance.Name,
		"restapi": map[string]interface{}{
			"listen": "0.0.0.0:8008",
		},
		"kubernetes": map[string]interface{}{
			"namespace":   instance.Namespace,
			"scope_label": naming.LabelInstance,
			"role_label":  naming.LabelRole,
			"labels": map[string]string{
				naming.LabelInstance: instance.Name,
			},
			"use_endpoints": true,
		},
		"bootstrap": map[string]interface{}{
			"dcs": map[string]interface{}{
				"ttl":       ttl,
				"loop_wait": loopWait,
				"postgresql": map[string]interface{}{
					"use_slots":  true,
					"parameters": parameters,
					"pg_hba":     hba,
				},
			},
		},
		"postgresql": map[string]interface{}{
			"listen":   fmt.Sprintf("0.0.0.0:%d", port),
			"data_dir": fmt.Sprintf("%s/pg%s", naming.PostgreSQLDataMountPath, engineVersion),
			"bin_dir":  fmt.Sprintf("/usr/lib/postgresql/%s/bin", engineVersion),
			"parameters": map[string]string{
				"unix_socket_directories": naming.PostgreSQLSocketDirectory,
			},
		},
	}, nil
}
