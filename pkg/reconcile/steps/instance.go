package steps

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/generate"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/security"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// reconcileInstance writes instance according to spec of cluster.
func reconcileMySQLInstance(rc *context.InstanceContext, runner *appsv1.StatefulSet) (err error) {
	runner.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("StatefulSet"))
	err = rc.SetControllerReference(runner)
	if err != nil {
		return
	}
	generate.InstanceStatefulSetIntent(rc, runner)
	// pvc
	err = reconcileDataVolume(rc, runner)
	if err != nil {
		return
	}
	err = reconcileLogVolume(rc, runner)
	if err != nil {
		return
	}
	generate.InstancePodIntent(rc, runner)
	err = errors.WithStack(rc.Apply(runner))
	return
}

// reconcileDataVolume writes the PersistentVolumeClaim for kdb instance data volume.
func reconcileDataVolume(rc *context.InstanceContext, runner *appsv1.StatefulSet) error {
	instance := rc.GetInstance()
	instanceVolumes := rc.Volumes()
	labelMap := map[string]string{
		naming.LabelClusterID:   naming.KDBInstanceClusterID(instance),
		naming.LabelInstanceSet: runner.Name,
		naming.LabelInstance:    instance.Name,
		naming.LabelData:        naming.Engine(instance),
	}

	var pvc *corev1.PersistentVolumeClaim
	existingPVCName, err := getPVCName(labelMap, instanceVolumes)
	if err != nil {
		return errors.WithStack(err)
	}
	if existingPVCName != "" {
		for i := range instanceVolumes {
			if instanceVolumes[i].Name == existingPVCName {
				current := instanceVolumes[i].Spec.Resources.Requests[corev1.ResourceStorage]
				desired := naming.InstanceDataPvcSpec(instance).Size
				if naming.IsPGEngine(instance) && desired.Cmp(current) < 0 {
					return errors.Errorf("PostgreSQL data PVC size cannot decrease from %s to %s", current.String(), desired.String())
				}
			}
		}
		pvc = &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Namespace: instance.GetNamespace(),
			Name:      existingPVCName,
		}}
	} else {
		pvc = &corev1.PersistentVolumeClaim{ObjectMeta: naming.InstanceDataVolume(runner)}
	}

	pvc.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PersistentVolumeClaim"))

	err = errors.WithStack(rc.SetControllerReference(pvc))
	dataPvcSpec := naming.InstanceDataPvcSpec(instance)
	instanceSet := naming.InstanceSetSpec(instance)
	pvc.Annotations = naming.Merge(
		instanceSet.Metadata.GetAnnotationsOrNil(),
		dataPvcSpec.Metadata.GetAnnotationsOrNil())

	pvc.Labels = naming.Merge(
		instanceSet.Metadata.GetLabelsOrNil(),
		dataPvcSpec.Metadata.GetLabelsOrNil(),
		labelMap,
	)
	pvc.Spec = corev1.PersistentVolumeClaimSpec{
		StorageClassName: &dataPvcSpec.StorageClass,
		AccessModes: []corev1.PersistentVolumeAccessMode{
			corev1.ReadWriteOnce,
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: dataPvcSpec.Size,
			},
		},
	}
	if err == nil {
		err = rc.HandlePersistentVolumeClaimError(errors.WithStack(rc.Apply(pvc)))
	}
	return err
}

// reconcileLogVolume writes the PersistentVolumeClaim for kdb instance log volume.
func reconcileLogVolume(rc *context.InstanceContext, runner *appsv1.StatefulSet) error {
	instance := rc.GetInstance()
	instanceSet := naming.InstanceSetSpec(instance)
	if instanceSet.LogVolumeClaimSpec == nil {
		return nil
	}
	instanceVolumes := rc.Volumes()
	labelMap := map[string]string{
		naming.LabelClusterID:   naming.KDBInstanceClusterID(instance),
		naming.LabelInstanceSet: runner.Name,
		naming.LabelInstance:    instance.Name,
		naming.LabelLog:         naming.Engine(instance),
	}

	var pvc *corev1.PersistentVolumeClaim
	existingPVCName, err := getPVCName(labelMap, instanceVolumes)
	if err != nil {
		return errors.WithStack(err)
	}
	if existingPVCName != "" {
		for i := range instanceVolumes {
			if instanceVolumes[i].Name == existingPVCName {
				current := instanceVolumes[i].Spec.Resources.Requests[corev1.ResourceStorage]
				desired := naming.InstanceLogPvcSpec(instance).Size
				if naming.IsPGEngine(instance) && desired.Cmp(current) < 0 {
					return errors.Errorf("PostgreSQL WAL PVC size cannot decrease from %s to %s", current.String(), desired.String())
				}
			}
		}
		pvc = &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Namespace: instance.GetNamespace(),
			Name:      existingPVCName,
		}}
	} else {
		pvc = &corev1.PersistentVolumeClaim{ObjectMeta: naming.InstanceLogVolume(runner)}
	}
	pvc.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PersistentVolumeClaim"))
	err = errors.WithStack(rc.SetControllerReference(pvc))
	logPvcSpec := naming.InstanceLogPvcSpec(instance)
	pvc.Annotations = naming.Merge(
		instanceSet.Metadata.GetAnnotationsOrNil(),
		logPvcSpec.Metadata.GetAnnotationsOrNil())

	pvc.Labels = naming.Merge(
		instanceSet.Metadata.GetLabelsOrNil(),
		logPvcSpec.Metadata.GetLabelsOrNil(),
		labelMap,
	)
	pvc.Spec = corev1.PersistentVolumeClaimSpec{
		StorageClassName: &logPvcSpec.StorageClass,
		AccessModes: []corev1.PersistentVolumeAccessMode{
			corev1.ReadWriteOnce,
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: logPvcSpec.Size,
			},
		},
	}
	if err == nil {
		err = rc.HandlePersistentVolumeClaimError(errors.WithStack(rc.Apply(pvc)))
	}

	return err
}

// getPVCName returns the name of a PVC that has the provided labels, if found.
func getPVCName(labelMap map[string]string,
	volumes []corev1.PersistentVolumeClaim,
) (string, error) {

	selector, err := naming.AsSelector(metav1.LabelSelector{
		MatchLabels: labelMap,
	})
	if err != nil {
		return "", errors.WithStack(err)
	}

	for _, pvc := range volumes {
		if selector.Matches(labels.Set(pvc.GetLabels())) {
			return pvc.GetName(), nil
		}
	}

	return "", nil
}

// reconcileInstance writes instance according to spec of cluster.
func reconcilePGInstance(rc *context.InstanceContext, runner *appsv1.StatefulSet) (err error) {
	runner.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("StatefulSet"))
	err = rc.SetControllerReference(runner)
	if err != nil {
		return
	}
	generate.InstanceStatefulSetIntent(rc, runner)
	err = reconcileDataVolume(rc, runner)
	if err != nil {
		return
	}
	err = reconcileLogVolume(rc, runner)
	if err != nil {
		return
	}
	postgresPodIntent(rc, runner)
	err = errors.WithStack(rc.Apply(runner))
	return
}

func postgresPodIntent(rc *context.InstanceContext, sts *appsv1.StatefulSet) {
	instance := rc.GetInstance()
	securityRevision := ""
	tlsSecret := &corev1.Secret{ObjectMeta: naming.PostgreSQLTLSSecret(instance)}
	if err := rc.Get(tlsSecret); err == nil {
		securityRevision = tlsSecret.ResourceVersion
	}
	credentialMeta := naming.PostgreSQLCredentialSecret(instance)
	if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.CredentialSecretRef != nil && instance.Spec.PostgreSQL.CredentialSecretRef.Name != "" {
		credentialMeta.Name = instance.Spec.PostgreSQL.CredentialSecretRef.Name
	}
	credentialSecret := &corev1.Secret{ObjectMeta: credentialMeta}
	if err := rc.Get(credentialSecret); err == nil {
		securityRevision += ":" + credentialSecret.ResourceVersion
	}
	if securityRevision != "" {
		if sts.Spec.Template.Annotations == nil {
			sts.Spec.Template.Annotations = map[string]string{}
		}
		sts.Spec.Template.Annotations["postgresql.kdb.com/security-revision"] = securityRevision
	}
	instanceSet := naming.InstanceSetSpec(instance)
	port := naming.KDBInstanceMasterPort(instance)
	if port == 0 {
		port = 5432
	}
	engineVersion := instance.Spec.EngineVersion
	if engineVersion == "" {
		engineVersion = "14"
	}
	pgData := naming.PostgreSQLDataMountPath + "/pg" + engineVersion
	pgWAL := pgData + "/pg_wal"
	if instanceSet.LogVolumeClaimSpec != nil {
		pgWAL = naming.PostgreSQLWALMountPath + "/pg" + engineVersion + "_wal"
	}
	pgBackRestRepo := postgresPGBackRestRepoPath(instance)
	restoreRepositoryPVC := ""
	if instance.Annotations != nil && instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Restore != nil &&
		postgresPGBackRestRepoType(instance) == "local" {
		restoreRepositoryPVC = strings.TrimSpace(instance.Annotations["kdb.com/restore-local-repository-pvc"])
	}
	var dr *v1.PostgreSQLDRSpec
	if instance.Spec.PostgreSQL != nil {
		dr = instance.Spec.PostgreSQL.DR
	}

	volumes := []corev1.Volume{
		{
			Name: "postgres-data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: naming.InstanceDataVolume(sts).Name,
					ReadOnly:  false,
				},
			},
		},
		{
			Name: "patroni-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: naming.InstanceConfigMap(instance).Name,
					},
				},
			},
		},
		{
			Name: "postgres-tmp",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "postgres-dshm",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumMemory,
				},
			},
		},
		{
			Name:         "postgres-runtime",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
		{
			Name:         "postgres-tls",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
		{
			Name: "postgres-tls-source",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: naming.PostgreSQLTLSSecret(instance).Name,
			}},
		},
	}
	if dr != nil && dr.Enabled && dr.Etcd3.SecretRef.Name != "" {
		mode := int32(0o440)
		volumes = append(volumes, corev1.Volume{
			Name: "postgresql-dr-etcd3",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName:  dr.Etcd3.SecretRef.Name,
				DefaultMode: &mode,
				Items: []corev1.KeyToPath{
					{Key: "ca.crt", Path: "ca.crt"},
					{Key: "tls.crt", Path: "tls.crt"},
					{Key: "tls.key", Path: "tls.key"},
				},
			}},
		})
	}
	if restoreRepositoryPVC != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "postgres-restore-repository",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: restoreRepositoryPVC,
					ReadOnly:  true,
				},
			},
		})
	}

	mounts := []corev1.VolumeMount{
		{Name: "postgres-data", MountPath: naming.PostgreSQLDataMountPath},
		{Name: "postgres-data", MountPath: naming.DataMountPath},
		{Name: "patroni-config", MountPath: naming.PatroniConfigMountPath, ReadOnly: true},
		{Name: "patroni-config", MountPath: naming.PGBackRestConfigMountPath, ReadOnly: true},
		{Name: "postgres-tmp", MountPath: "/tmp"},
		{Name: "postgres-dshm", MountPath: "/dev/shm"},
		{Name: "postgres-runtime", MountPath: "/var/run/kdb-ha"},
		{Name: "postgres-tls", MountPath: "/etc/postgresql/tls"},
	}
	if dr != nil && dr.Enabled && dr.Etcd3.SecretRef.Name != "" {
		mounts = append(mounts, corev1.VolumeMount{Name: "postgresql-dr-etcd3", MountPath: "/etc/postgresql/dr-etcd3", ReadOnly: true})
	}
	if postgresPGBackRestEnabled(instance) && postgresPGBackRestRepoType(instance) == "local" {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "postgres-data",
			MountPath: postgresPGBackRestRepoPath(instance),
			SubPath:   "pgbackrestrepo",
		})
	}
	if instanceSet.LogVolumeClaimSpec != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "postgres-wal",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: naming.InstanceLogVolume(sts).Name,
					ReadOnly:  false,
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: "postgres-wal", MountPath: naming.PostgreSQLWALMountPath})
	}

	envs := []corev1.EnvVar{
		{Name: "PGDATA", Value: pgData},
		{Name: "PGHOST", Value: naming.PostgreSQLSocketDirectory},
		{Name: "PGPORT", Value: fmt.Sprint(port)},
		{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		{Name: "POD_IP", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"}}},
		{Name: "POD_UID", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"}}},
		{Name: "PATRONI_NAME", Value: "$(POD_NAME)"},
		{Name: "PATRONI_POSTGRESQL_CONNECT_ADDRESS", Value: fmt.Sprintf("$(POD_IP):%d", port)},
		{Name: "PATRONI_POSTGRESQL_DATA_DIR", Value: pgData},
		{Name: "PATRONI_POSTGRESQL_LISTEN", Value: fmt.Sprintf("*:%d", port)},
		{Name: "PATRONI_RESTAPI_CONNECT_ADDRESS", Value: "$(POD_IP):8008"},
		{Name: "PATRONI_RESTAPI_LISTEN", Value: "*:8008"},
		{Name: "PATRONICTL_CONFIG_FILE", Value: fmt.Sprintf("%s/%s", naming.PatroniConfigMountPath, naming.PatroniConfigKey)},
		postgresCredentialEnv(instance, "PATRONI_POSTGRESQL_AUTHENTICATION_SUPERUSER_USERNAME", naming.PostgreSQLSuperuserUsernameKey),
		postgresCredentialEnv(instance, "PATRONI_POSTGRESQL_AUTHENTICATION_SUPERUSER_PASSWORD", naming.PostgreSQLSuperuserPasswordKey),
		postgresCredentialEnv(instance, "PATRONI_POSTGRESQL_AUTHENTICATION_REPLICATION_USERNAME", naming.PostgreSQLReplicationUsernameKey),
		postgresCredentialEnv(instance, "PATRONI_POSTGRESQL_AUTHENTICATION_REPLICATION_PASSWORD", naming.PostgreSQLReplicationPasswordKey),
		postgresCredentialEnv(instance, "PATRONI_POSTGRESQL_AUTHENTICATION_BACKUP_USERNAME", naming.PostgreSQLBackupUsernameKey),
		postgresCredentialEnv(instance, "PATRONI_POSTGRESQL_AUTHENTICATION_BACKUP_PASSWORD", naming.PostgreSQLBackupPasswordKey),
		postgresCredentialEnv(instance, "PATRONI_POSTGRESQL_AUTHENTICATION_MONITORING_USERNAME", naming.PostgreSQLMonitoringUsernameKey),
		postgresCredentialEnv(instance, "PATRONI_POSTGRESQL_AUTHENTICATION_MONITORING_PASSWORD", naming.PostgreSQLMonitoringPasswordKey),
		postgresCredentialEnv(instance, "PATRONI_RESTAPI_USERNAME", naming.PostgreSQLRESTAPIUsernameKey),
		postgresCredentialEnv(instance, "PATRONI_RESTAPI_PASSWORD", naming.PostgreSQLRESTAPIPasswordKey),
		{Name: "KDB_INSTANCE_NAME", Value: instance.Name},
		{Name: "KDB_NAMESPACE", Value: instance.Namespace},
		{Name: "KDB_ENGINE", Value: naming.Engine(instance)},
	}
	splitRuntime := naming.PostgreSQLSplitRuntime(instance)
	if splitRuntime {
		envs = append(envs,
			corev1.EnvVar{Name: "KDB_HA_POSTGRESQL_RUNTIME_SOCKET", Value: "/var/run/kdb-ha/postgres-runtime.sock"},
			corev1.EnvVar{Name: "KDB_HA_POSTGRESQL_TOOL_SOCKET", Value: "/var/run/kdb-ha/postgres-tools.sock"},
		)
	}
	if postgresPGBackRestEnabled(instance) {
		envs = append(envs, corev1.EnvVar{Name: "PGBACKREST_STANZA", Value: postgresPGBackRestStanza(instance)})
	}
	if postgresPGBackRestEnabled(instance) && postgresPGBackRestRepoType(instance) == "s3" {
		envs = append(envs,
			postgresPGBackRestSecretEnv(instance, "PGBACKREST_REPO1_S3_KEY", naming.PostgreSQLPGBackRestS3Key, false),
			postgresPGBackRestSecretEnv(instance, "PGBACKREST_REPO1_S3_KEY_SECRET", naming.PostgreSQLPGBackRestS3SecretKey, false),
			postgresPGBackRestSecretEnv(instance, "PGBACKREST_REPO1_S3_TOKEN", naming.PostgreSQLPGBackRestS3TokenKey, true),
			postgresPGBackRestSecretEnv(instance, "PGBACKREST_REPO1_CIPHER_PASS", naming.PostgreSQLPGBackRestCipherPassKey, false),
		)
	}
	if dr != nil && dr.Enabled && dr.Etcd3.SecretRef.Name != "" {
		optional := true
		envs = append(envs,
			corev1.EnvVar{Name: "KDB_HA_DR_DCS_USERNAME", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: dr.Etcd3.SecretRef, Key: "username", Optional: &optional}}},
			corev1.EnvVar{Name: "KDB_HA_DR_DCS_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: dr.Etcd3.SecretRef, Key: "password", Optional: &optional}}},
		)
	}
	if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Restore != nil {
		envs = append(envs, corev1.EnvVar{Name: "KDB_RESTORE_BOOTSTRAP", Value: "true"})
	}

	databaseEnvs := envs
	databaseMounts := mounts
	database := corev1.Container{
		Name:            naming.ContainerDatabase,
		Command:         []string{"/bin/sh", "-c", "exec sleep infinity"},
		Env:             append(databaseEnvs, instanceSet.MainContainer.Env...),
		Image:           instanceSet.MainContainer.Image,
		Resources:       instanceSet.MainContainer.Resources,
		SecurityContext: security.InitLegacySecurityContext(),
		Ports:           []corev1.ContainerPort{{Name: naming.PortDatabase, ContainerPort: port, Protocol: corev1.ProtocolTCP}},
		VolumeMounts:    databaseMounts,
	}
	if splitRuntime {
		databaseEnvs = []corev1.EnvVar{
			{Name: "PGDATA", Value: pgData},
			{Name: "PGHOST", Value: naming.PostgreSQLSocketDirectory},
			{Name: "PGPORT", Value: fmt.Sprint(port)},
		}
		if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Restore != nil {
			databaseEnvs = append(databaseEnvs, corev1.EnvVar{Name: "KDB_RESTORE_BOOTSTRAP", Value: "true"})
		}
		databaseMounts = postgresDatabaseVolumeMounts(mounts)
		database.Command = []string{"kdb-pg-runtime"}
		database.Args = []string{"--socket=/var/run/kdb-ha/postgres-runtime.sock"}
		database.Env = append(databaseEnvs, instanceSet.MainContainer.Env...)
		database.VolumeMounts = databaseMounts
		database.Lifecycle = &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{Command: []string{
					fmt.Sprintf("/usr/lib/postgresql/%s/bin/pg_ctl", engineVersion),
					"stop", "-D", pgData, "-m", "fast", "-w",
				}},
			},
		}
	}
	kdbHA := corev1.Container{
		Name:            naming.ContainerPostgreSQLHA,
		Command:         []string{"kdb-ha"},
		Args:            []string{fmt.Sprintf("%s/%s", naming.PatroniConfigMountPath, naming.PatroniConfigKey)},
		Env:             append(envs, instanceSet.SidecarContainer.Env...),
		Image:           instanceSet.SidecarContainer.Image,
		Resources:       instanceSet.SidecarContainer.Resources,
		SecurityContext: security.InitLegacySecurityContext(),
		Ports:           []corev1.ContainerPort{{Name: naming.PortPostgreSQLHA, ContainerPort: 8008, Protocol: corev1.ProtocolTCP}},
		VolumeMounts:    mounts,
		LivenessProbe: &corev1.Probe{
			InitialDelaySeconds: 30,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			FailureThreshold:    6,
			ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString(naming.PortPostgreSQLHA)}},
		},
		ReadinessProbe: &corev1.Probe{
			InitialDelaySeconds: 10,
			PeriodSeconds:       5,
			TimeoutSeconds:      5,
			FailureThreshold:    6,
			ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString(naming.PortPostgreSQLHA)}},
		},
	}
	if !splitRuntime {
		if len(instanceSet.MainContainer.Command) > 0 {
			database.Command = instanceSet.MainContainer.Command
		}
		if len(instanceSet.MainContainer.Args) > 0 {
			database.Args = instanceSet.MainContainer.Args
		}
		if len(instanceSet.SidecarContainer.Command) > 0 {
			kdbHA.Command = instanceSet.SidecarContainer.Command
		}
		if len(instanceSet.SidecarContainer.Args) > 0 {
			kdbHA.Args = instanceSet.SidecarContainer.Args
		}
	}
	containers := []corev1.Container{database}
	if kdbHA.Image != "" {
		containers = append(containers, kdbHA)
	}
	if postgresExporterEnabled(instance) {
		containers = append(containers, postgresExporterContainer(instance, instanceSet, mounts))
	}

	startup := corev1.Container{
		Name: "postgres-startup",
		Command: []string{
			"bash",
			"-ceu",
			fmt.Sprintf("install -d %s %s %s %s %s %s /etc/postgresql/tls && cp /etc/postgresql/tls-source/* /etc/postgresql/tls/ && chmod 0600 /etc/postgresql/tls/tls.key /etc/postgresql/tls/client.key && chmod 0644 /etc/postgresql/tls/ca.crt /etc/postgresql/tls/tls.crt /etc/postgresql/tls/client.crt && chown -R 100:102 %s %s %s %s %s %s /etc/postgresql/tls && exec /kdb/bin/postgres-startup.sh %s %s",
				naming.PostgreSQLDataMountPath,
				pgData,
				naming.DatabaseLogRoot,
				naming.PostgreSQLWALMountPath,
				naming.PostgreSQLSocketDirectory,
				pgBackRestRepo,
				naming.PostgreSQLDataMountPath,
				pgData,
				naming.DatabaseLogRoot,
				naming.PostgreSQLWALMountPath,
				naming.PostgreSQLSocketDirectory,
				pgBackRestRepo,
				engineVersion,
				pgWAL),
		},
		Env:             databaseEnvs,
		Image:           instanceSet.MainContainer.Image,
		Resources:       instanceSet.MainContainer.Resources,
		SecurityContext: security.InitSecurityContextForStartUp(),
		VolumeMounts:    append(postgresStartupVolumeMounts(databaseMounts, mounts, instance), corev1.VolumeMount{Name: "postgres-tls-source", MountPath: "/etc/postgresql/tls-source", ReadOnly: true}),
	}
	startup.SecurityContext.RunAsUser = util.Int64(0)

	fsGroupPolicy := corev1.FSGroupChangeOnRootMismatch
	sts.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
		FSGroup:             util.Int64(102),
		FSGroupChangePolicy: &fsGroupPolicy,
		SupplementalGroups:  instance.Spec.SupplementalGroups,
	}
	sts.Spec.Template.Spec.Volumes = volumes
	initContainers := []corev1.Container{}
	restoreMounts := mounts
	if restoreRepositoryPVC != "" {
		restoreMounts = append(append([]corev1.VolumeMount{}, mounts...), corev1.VolumeMount{
			Name:      "postgres-restore-repository",
			MountPath: "/var/lib/kdb/restore-repository-source",
			SubPath:   "pgbackrestrepo",
			ReadOnly:  true,
		})
	}
	if restore := postgresRestoreContainer(instance, instanceSet, envs, restoreMounts); restore != nil {
		initContainers = append(initContainers, *restore)
	}
	initContainers = append(initContainers, startup)
	sts.Spec.Template.Spec.InitContainers = initContainers
	sts.Spec.Template.Spec.Containers = containers
}

func postgresPGBackRestStanza(instance *v1.KDBInstance) string {
	if instance != nil && instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Backups != nil && instance.Spec.PostgreSQL.Backups.PGBackRest != nil && instance.Spec.PostgreSQL.Backups.PGBackRest.Stanza != "" {
		return instance.Spec.PostgreSQL.Backups.PGBackRest.Stanza
	}
	return "db"
}

func postgresPGBackRestSecretEnv(instance *v1.KDBInstance, name, key string, optional bool) corev1.EnvVar {
	secretName := ""
	if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Backups != nil && instance.Spec.PostgreSQL.Backups.PGBackRest != nil && instance.Spec.PostgreSQL.Backups.PGBackRest.RepoSecretRef != nil {
		secretName = instance.Spec.PostgreSQL.Backups.PGBackRest.RepoSecretRef.Name
	}
	return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: key, Optional: &optional}}}
}

func postgresRestoreContainer(instance *v1.KDBInstance, instanceSet shared.InstanceSetSpec, envs []corev1.EnvVar, mounts []corev1.VolumeMount) *corev1.Container {
	if instance.Spec.PostgreSQL == nil || instance.Spec.PostgreSQL.Restore == nil {
		return nil
	}
	restore := instance.Spec.PostgreSQL.Restore
	args := []string{"pgbackrest", "--config=" + naming.PGBackRestConfigMountPath + "/" + naming.PGBackRestConfigKey, "--stanza=" + postgresPGBackRestStanza(instance), "--delta"}
	if restore.BackupID != "" {
		args = append(args, "--set="+restore.BackupID)
	}
	if restore.TargetType != "" {
		args = append(args, "--type="+restore.TargetType)
		if restore.Target != "" {
			args = append(args, "--target="+restore.Target)
		}
	}
	if restore.TargetAction != "" {
		args = append(args, "--target-action="+restore.TargetAction)
	}
	if naming.PostgreSQLSplitRuntime(instance) {
		args = append(args, "--recovery-option=restore_command="+fmt.Sprintf(
			"kdb-pg-tool --socket=/var/run/kdb-ha/postgres-tools.sock pgbackrest --config=%s/%s --stanza=%s archive-get %%f %%p",
			naming.PGBackRestConfigMountPath,
			naming.PGBackRestConfigKey,
			postgresPGBackRestStanza(instance),
		))
	}
	args = append(args, "restore")
	restoreEnvs := append([]corev1.EnvVar{}, envs...)
	if instance.Annotations != nil && strings.TrimSpace(instance.Annotations["kdb.com/restore-local-repository-pvc"]) != "" {
		restoreEnvs = append(restoreEnvs,
			corev1.EnvVar{Name: "KDB_LOCAL_RESTORE_REPOSITORY_SOURCE", Value: "/var/lib/kdb/restore-repository-source"},
			corev1.EnvVar{Name: "KDB_LOCAL_RESTORE_REPOSITORY_TARGET", Value: postgresPGBackRestRepoPath(instance)},
		)
	}
	return &corev1.Container{
		Name: "pgbackrest-restore", Image: instanceSet.SidecarContainer.Image,
		Command: []string{"bash", "-ceu"},
		Args: append([]string{`marker=/var/run/kdb-ha/restore-mode
touch "$marker"
install -d "$PGDATA"
if [ ! -s "$PGDATA/PG_VERSION" ]; then
  if [ -n "${KDB_LOCAL_RESTORE_REPOSITORY_SOURCE:-}" ]; then
    test -s "$KDB_LOCAL_RESTORE_REPOSITORY_SOURCE/backup/$PGBACKREST_STANZA/backup.info"
    cp -R "$KDB_LOCAL_RESTORE_REPOSITORY_SOURCE/." "$KDB_LOCAL_RESTORE_REPOSITORY_TARGET/"
  fi
  "$@"
fi
test -s "$PGDATA/PG_VERSION"
rm -f "$marker"`, "--"}, args...),
		Env: append(restoreEnvs, instanceSet.SidecarContainer.Env...), Resources: instanceSet.SidecarContainer.Resources,
		SecurityContext: security.InitLegacySecurityContext(), VolumeMounts: mounts,
	}
}

func postgresDatabaseVolumeMounts(mounts []corev1.VolumeMount) []corev1.VolumeMount {
	result := make([]corev1.VolumeMount, 0, len(mounts))
	for _, mount := range mounts {
		switch mount.Name {
		case "postgres-data":
			if mount.MountPath == naming.PostgreSQLDataMountPath || mount.MountPath == naming.DataMountPath {
				result = append(result, mount)
			}
		case "postgres-wal", "postgres-tmp", "postgres-dshm", "postgres-runtime", "postgres-tls":
			result = append(result, mount)
		}
	}
	return result
}

func postgresStartupVolumeMounts(databaseMounts, allMounts []corev1.VolumeMount, instance *v1.KDBInstance) []corev1.VolumeMount {
	result := append([]corev1.VolumeMount{}, databaseMounts...)
	if !postgresPGBackRestEnabled(instance) || postgresPGBackRestRepoType(instance) != "local" {
		return result
	}
	repositoryPath := postgresPGBackRestRepoPath(instance)
	for _, mount := range allMounts {
		if mount.Name == "postgres-data" && mount.MountPath == repositoryPath {
			return append(result, mount)
		}
	}
	return result
}

func postgresCredentialEnv(instance *v1.KDBInstance, name, key string) corev1.EnvVar {
	secretName := naming.PostgreSQLCredentialSecret(instance).Name
	if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.CredentialSecretRef != nil &&
		instance.Spec.PostgreSQL.CredentialSecretRef.Name != "" {
		secretName = instance.Spec.PostgreSQL.CredentialSecretRef.Name
	}
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  key,
			},
		},
	}
}

func postgresPGBackRestEnabled(instance *v1.KDBInstance) bool {
	return instance != nil &&
		instance.Spec.PostgreSQL != nil &&
		instance.Spec.PostgreSQL.Backups != nil &&
		instance.Spec.PostgreSQL.Backups.PGBackRest != nil &&
		instance.Spec.PostgreSQL.Backups.PGBackRest.Enabled
}

func postgresPGBackRestRepoType(instance *v1.KDBInstance) string {
	if instance != nil && instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Backups != nil &&
		instance.Spec.PostgreSQL.Backups.PGBackRest != nil && instance.Spec.PostgreSQL.Backups.PGBackRest.RepoType != "" {
		return instance.Spec.PostgreSQL.Backups.PGBackRest.RepoType
	}
	return "local"
}

func postgresPGBackRestRepoPath(instance *v1.KDBInstance) string {
	if instance != nil && instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.Backups != nil &&
		instance.Spec.PostgreSQL.Backups.PGBackRest != nil && instance.Spec.PostgreSQL.Backups.PGBackRest.RepoPath != "" {
		return instance.Spec.PostgreSQL.Backups.PGBackRest.RepoPath
	}
	return "/backrestrepo"
}

func postgresExporterEnabled(instance *v1.KDBInstance) bool {
	return instance != nil &&
		instance.Spec.PostgreSQL != nil &&
		instance.Spec.PostgreSQL.Exporter != nil &&
		instance.Spec.PostgreSQL.Exporter.Enabled
}

func postgresExporterContainer(instance *v1.KDBInstance, instanceSet shared.InstanceSetSpec, mounts []corev1.VolumeMount) corev1.Container {
	monitor := instanceSet.MonitorContainer
	exporter := instance.Spec.PostgreSQL.Exporter
	if exporter.Image != "" {
		monitor.Image = exporter.Image
	}
	if len(exporter.Env) > 0 {
		monitor.Env = append(monitor.Env, exporter.Env...)
	}
	if !isEmptyResourceRequirements(exporter.Resources) {
		monitor.Resources = exporter.Resources
	}
	if postgreSQLMajorAtLeast(instance, 17) && !containsString(monitor.Args, "--no-collector.stat_bgwriter") {
		// postgres_exporter v0.15 reads columns removed from pg_stat_bgwriter
		// in PostgreSQL 17. Keep the remaining default and extended collectors
		// healthy until the offline image baseline can be upgraded.
		monitor.Args = append(monitor.Args, "--no-collector.stat_bgwriter")
	}
	monitor.Env = append([]corev1.EnvVar{
		{Name: "DATA_SOURCE_URI", Value: fmt.Sprintf("localhost:%d/postgres?sslmode=verify-ca&sslrootcert=/etc/postgresql/tls/ca.crt&sslcert=/etc/postgresql/tls/client.crt&sslkey=/etc/postgresql/tls/client.key", postgresPort(instance))},
		postgresCredentialEnv(instance, "DATA_SOURCE_USER", naming.PostgreSQLMonitoringUsernameKey),
		postgresCredentialEnv(instance, "DATA_SOURCE_PASS", naming.PostgreSQLMonitoringPasswordKey),
		{Name: "PG_EXPORTER_EXTEND_QUERY_PATH", Value: naming.PatroniConfigMountPath + "/postgres-exporter-queries.yaml"},
	}, monitor.Env...)
	exporterSecurityContext := security.InitLegacySecurityContext()
	// postgres-startup deliberately owns the client key as postgres:postgres
	// with mode 0600. Running the exporter as the same non-root UID is the only
	// lib/pq-compatible way to read it without weakening private-key modes.
	exporterSecurityContext.RunAsUser = util.Int64(100)
	exporterSecurityContext.RunAsGroup = util.Int64(102)
	exporterSecurityContext.RunAsNonRoot = util.Bool(true)
	return corev1.Container{
		Name:      naming.ContainerPostgreSQLExporter,
		Command:   monitor.Command,
		Args:      monitor.Args,
		Env:       monitor.Env,
		Image:     monitor.Image,
		Resources: monitor.Resources,
		Ports: []corev1.ContainerPort{{
			Name:          naming.PortPostgreSQLMetrics,
			ContainerPort: 9187,
			Protocol:      corev1.ProtocolTCP,
		}},
		SecurityContext: exporterSecurityContext,
		VolumeMounts:    postgresExporterVolumeMounts(mounts),
	}
}

func postgreSQLMajorAtLeast(instance *v1.KDBInstance, minimum int) bool {
	if instance == nil {
		return false
	}
	majorText := strings.TrimSpace(strings.SplitN(instance.Spec.EngineVersion, ".", 2)[0])
	major, err := strconv.Atoi(majorText)
	return err == nil && major >= minimum
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func postgresPort(instance *v1.KDBInstance) int32 {
	if port := naming.KDBInstanceMasterPort(instance); port > 0 {
		return port
	}
	return 5432
}

func postgresExporterVolumeMounts(mounts []corev1.VolumeMount) []corev1.VolumeMount {
	out := make([]corev1.VolumeMount, 0, 3)
	for _, mount := range mounts {
		switch mount.Name {
		case "postgres-tmp", "postgres-tls":
			mount.ReadOnly = mount.Name != "postgres-tmp"
			out = append(out, mount)
		case "patroni-config":
			if mount.MountPath == naming.PatroniConfigMountPath {
				mount.ReadOnly = true
				out = append(out, mount)
			}
		}
	}
	return out
}
