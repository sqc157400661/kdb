package mysql

import (
	stdcontext "context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/pkg/errors"
	"github.com/sqc157400661/helper/kube"
	"github.com/sqc157400661/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/config"
	"github.com/sqc157400661/kdb/internal/naming"
	internalsecurity "github.com/sqc157400661/kdb/internal/security"
	"github.com/sqc157400661/kdb/internal/topology"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps"
)

type InstanceStepManager struct {
	steps.InstanceStepManager
}

const mysqlMGRRecoveryPasswordMaxLength = 32

// SetInstanceConfig set mysql and sidecar config
// TODO: processing parameters that require a restart to take effect
func (s *InstanceStepManager) SetInstanceConfig() kube.BindFunc {
	return s.StepBinder(
		"SetInstanceConfig",
		func(rc *context.InstanceContext, flow kube.Flow) (reconcile.Result, error) {
			instance := rc.GetInstance()
			instanceConfigMap := &corev1.ConfigMap{ObjectMeta: naming.InstanceConfigMap(instance)}
			instanceConfigMap.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))

			err := errors.WithStack(rc.SetControllerReference(instanceConfigMap))

			if err != nil {
				return flow.Error(err, "Set Reference err")
			}
			instanceConfigMap.Annotations = instance.Annotations
			instanceConfigMap.Labels = naming.Merge(instance.Labels,
				map[string]string{
					naming.LabelInstance: instance.Name,
				})
			globalConfig := rc.GetGlobalConfig()
			credentialSecret := &corev1.Secret{
				ObjectMeta: naming.MySQLCredentialSecret(instance),
				Type:       corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					naming.MySQLRootPasswordSecretKey:        []byte(globalConfig.DB.RootPassword),
					naming.MySQLReplicationPasswordSecretKey: []byte(globalConfig.DB.ReplPassword),
				},
			}
			existingCredential := &corev1.Secret{}
			getCredentialErr := rc.Client().Get(rc.Context(), client.ObjectKeyFromObject(credentialSecret), existingCredential)
			switch {
			case getCredentialErr == nil &&
				len(existingCredential.Data["ca.crt"]) > 0 &&
				len(existingCredential.Data["tls.crt"]) > 0 &&
				len(existingCredential.Data["tls.key"]) > 0 &&
				len(existingCredential.Data["client.crt"]) > 0 &&
				len(existingCredential.Data["client.key"]) > 0:
				credentialSecret.Data["ca.crt"] = existingCredential.Data["ca.crt"]
				credentialSecret.Data["tls.crt"] = existingCredential.Data["tls.crt"]
				credentialSecret.Data["tls.key"] = existingCredential.Data["tls.key"]
				credentialSecret.Data["client.crt"] = existingCredential.Data["client.crt"]
				credentialSecret.Data["client.key"] = existingCredential.Data["client.key"]
			case getCredentialErr == nil || apierrors.IsNotFound(getCredentialErr):
				bundle, tlsErr := internalsecurity.GenerateRuntimeMTLSBundle(instance, "MySQL sidecar")
				if tlsErr != nil {
					return flow.Error(tlsErr, "generate MySQL sidecar TLS identity err")
				}
				credentialSecret.Data["ca.crt"] = bundle.CA
				credentialSecret.Data["tls.crt"], credentialSecret.Data["tls.key"] = bundle.ServerCert, bundle.ServerKey
				credentialSecret.Data["client.crt"], credentialSecret.Data["client.key"] = bundle.ClientCert, bundle.ClientKey
			default:
				return flow.Error(getCredentialErr, "get MySQL credential Secret err")
			}
			existingRootPassword, existingReplicationPassword := []byte(nil), []byte(nil)
			existingMonitorPassword, existingProxyPassword := []byte(nil), []byte(nil)
			if getCredentialErr == nil {
				existingRootPassword = existingCredential.Data[naming.MySQLRootPasswordSecretKey]
				existingReplicationPassword = existingCredential.Data[naming.MySQLReplicationPasswordSecretKey]
				existingMonitorPassword = existingCredential.Data[naming.MySQLMonitorPasswordSecretKey]
				existingProxyPassword = existingCredential.Data[naming.MySQLProxyPasswordSecretKey]
			}
			rootPassword, passwordErr := resolveMySQLRootPassword(rc.Context(), rc.Client(), instance, globalConfig.DB.RootPassword, existingRootPassword)
			if passwordErr != nil {
				return flow.Error(passwordErr, "resolve MySQL root credential err")
			}
			replicationPassword, passwordErr := resolveMySQLReplicationPassword(instance, globalConfig.DB.ReplPassword, existingReplicationPassword)
			if passwordErr != nil {
				return flow.Error(passwordErr, "resolve MySQL replication credential err")
			}
			monitorPassword, passwordErr := resolveMySQLCredentialValue("", existingMonitorPassword)
			if passwordErr != nil {
				return flow.Error(passwordErr, "resolve MySQL monitor credential err")
			}
			proxyPassword, passwordErr := resolveMySQLCredentialValue("", existingProxyPassword)
			if passwordErr != nil {
				return flow.Error(passwordErr, "resolve MySQL proxy credential err")
			}
			credentialSecret.Data[naming.MySQLRootPasswordSecretKey] = rootPassword
			credentialSecret.Data[naming.MySQLReplicationPasswordSecretKey] = replicationPassword
			credentialSecret.Data[naming.MySQLMonitorPasswordSecretKey] = monitorPassword
			credentialSecret.Data[naming.MySQLProxyPasswordSecretKey] = proxyPassword
			credentialSecret.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
			if err := errors.WithStack(rc.SetControllerReference(credentialSecret)); err != nil {
				return flow.Error(err, "Set MySQL credential Secret reference err")
			}
			if err := errors.WithStack(rc.Apply(credentialSecret)); err != nil {
				return flow.Error(err, "apply MySQL credential Secret err")
			}
			projectionUpdated, err := stageMySQLCredentialProjection(rc, instance)
			if err != nil {
				return flow.Error(err, "stage MySQL credential projection err")
			}
			if projectionUpdated {
				// Existing StatefulSets use OnDelete. Publish the new Secret keys in
				// their Pod templates before any ConfigMap starts referencing the
				// corresponding files; the next reconcile can then roll Pods safely.
				return flow.RetryAfter(2*time.Second, "mysql credential projection staged")
			}
			replications, err := resolveReplications(rc, instance, globalConfig.GetHostResolveMode(), globalConfig.DB.ReplUser, naming.MySQLReplicationPasswordPath)
			if err != nil {
				return flow.Error(err, "resolve replications err")
			}
			mgrConfig, err := topology.ResolveMGRConfig(instance)
			if err != nil {
				return flow.Error(err, "resolve mgr config err")
			}
			mgrLocalAddress := ""
			if mgrConfig.Enabled {
				mgrLocalAddress = topology.BuildMGRLocalAddress(instance, int(mgrConfig.BootstrapOrdinal), mgrConfig.GroupPort)
			}
			parameterReport := globalConfig.GetParameterReportConfig()
			backupTemplate := resolveBackupTemplateConfig(instance.Spec.Config)
			// create config
			util.StringMap(&instanceConfigMap.Data)
			templateData := map[string]any{
				"RootUser":                       globalConfig.DB.RootUser,
				"RootPasswordFile":               naming.MySQLRootPasswordPath,
				"MonitorPasswordFile":            naming.MySQLMonitorPasswordPath,
				"ProxyPasswordFile":              naming.MySQLProxyPasswordPath,
				"ReplUser":                       globalConfig.DB.ReplUser,
				"ReplPasswordFile":               naming.MySQLReplicationPasswordPath,
				"CurrentVersion":                 naming.CurrentConfigVersion(instance),
				"UpdateVersion":                  naming.UpdateConfigVersion(instance),
				"Replications":                   replications,
				"DeployArch":                     naming.DeployArch(instance),
				"MGREnabled":                     mgrConfig.Enabled,
				"MGRMode":                        mgrConfig.Mode,
				"MGRGroupName":                   mgrConfig.GroupName,
				"MGRLocalAddress":                mgrLocalAddress,
				"MGRSeeds":                       mgrConfig.Seeds,
				"MGRBootstrap":                   mgrConfig.Enabled,
				"MGRBootstrapOrdinal":            mgrConfig.BootstrapOrdinal,
				"MGRGroupPort":                   mgrConfig.GroupPort,
				"ParameterReportEnabled":         parameterReport.Enabled,
				"ParameterReportHostFile":        parameterReport.HostFile,
				"ParameterReportTokenFile":       parameterReport.TokenFile,
				"ParameterReportCatalogFile":     parameterReport.CatalogFile,
				"ParameterReportIntervalSeconds": parameterReport.IntervalSeconds,
				"ParameterReportTimeoutSeconds":  parameterReport.TimeoutSeconds,
			}
			for key, value := range backupTemplate {
				templateData[key] = value
			}
			configStr, err := util.SafeTemplateFill(config.InstanceConfigTmpl, templateData)
			if err != nil {
				return flow.Error(err, "get instance config err")
			}
			instanceConfigMap.Data[naming.SidecarConfigKey] = naming.YamlGeneratedWarning + configStr
			v1, err := naming.EngineVersion(instance)
			if err != nil {
				return flow.Error(err, "get instance version err")
			}
			v2, _ := version.NewVersion("8.0")
			// TODO: 根据内存和cpu动态调整 tmpl配置的内容
			if v1.GreaterThanOrEqual(v2) {
				instanceConfigMap.Data[naming.DatabaseConfigKey] = naming.YamlGeneratedWarning + config.MySQL8ConfTmpl
			} else {
				instanceConfigMap.Data[naming.DatabaseConfigKey] = naming.YamlGeneratedWarning + config.MySQL57ConfTmpl
			}
			err = errors.WithStack(rc.Apply(instanceConfigMap))
			if err != nil {
				return flow.Error(err, "apply err")
			}
			if err := reconcileMySQLAllowlistNetworkPolicy(rc, instance); err != nil {
				return flow.Error(err, "reconcile mysql allowlist NetworkPolicy err")
			}
			rc.SetInstanceConfigMap(instanceConfigMap)
			return flow.Pass()
		})
}

func stageMySQLCredentialProjection(rc *context.InstanceContext, instance *v1.KDBInstance) (bool, error) {
	if rc == nil || instance == nil || instance.Spec.InstanceSet.Replicas == nil {
		return false, nil
	}
	updated := false
	for ordinal := 0; ordinal < int(*instance.Spec.InstanceSet.Replicas); ordinal++ {
		sts := &appsv1.StatefulSet{}
		key := client.ObjectKey{
			Namespace: instance.Namespace,
			Name:      naming.InstanceStatefulSetName(instance.Name, ordinal),
		}
		if err := rc.Client().Get(rc.Context(), key, sts); err != nil {
			if apierrors.IsNotFound(err) {
				// A new StatefulSet will be created from the complete desired Pod
				// template later in this reconcile, so no compatibility pass is needed.
				continue
			}
			return false, err
		}
		before := sts.DeepCopy()
		if !ensureMySQLCredentialProjectionItems(sts, naming.MySQLCredentialSecret(instance).Name) {
			continue
		}
		if err := rc.Client().Patch(rc.Context(), sts, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return false, err
		}
		updated = true
	}
	return updated, nil
}

func ensureMySQLCredentialProjectionItems(sts *appsv1.StatefulSet, secretName string) bool {
	if sts == nil || strings.TrimSpace(secretName) == "" {
		return false
	}
	desired := []corev1.KeyToPath{
		{Key: naming.MySQLMonitorPasswordSecretKey, Path: naming.MySQLMonitorPasswordProjectionPath},
		{Key: naming.MySQLProxyPasswordSecretKey, Path: naming.MySQLProxyPasswordProjectionPath},
	}
	for volumeIndex := range sts.Spec.Template.Spec.Volumes {
		projected := sts.Spec.Template.Spec.Volumes[volumeIndex].Projected
		if projected == nil {
			continue
		}
		for sourceIndex := range projected.Sources {
			secret := projected.Sources[sourceIndex].Secret
			if secret == nil || secret.Name != secretName {
				continue
			}
			existing := make(map[string]struct{}, len(secret.Items))
			for _, item := range secret.Items {
				existing[item.Key] = struct{}{}
			}
			changed := false
			for _, item := range desired {
				if _, found := existing[item.Key]; found {
					continue
				}
				secret.Items = append(secret.Items, item)
				changed = true
			}
			return changed
		}
	}
	return false
}

func resolveMySQLCredentialValue(configured string, existing []byte) ([]byte, error) {
	if strings.TrimSpace(configured) != "" {
		return []byte(configured), nil
	}
	if strings.TrimSpace(string(existing)) != "" {
		return append([]byte(nil), existing...), nil
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate credential: %w", err)
	}
	encoded := make([]byte, hex.EncodedLen(len(raw)))
	hex.Encode(encoded, raw)
	return encoded, nil
}

func resolveMySQLReplicationPassword(instance *v1.KDBInstance, configured string, existing []byte) ([]byte, error) {
	password, err := resolveMySQLCredentialValue(configured, existing)
	if err != nil {
		return nil, err
	}
	if instance == nil || instance.Spec.DeployArch != "MGR" || len(password) <= mysqlMGRRecoveryPasswordMaxLength {
		return password, nil
	}
	if strings.TrimSpace(configured) != "" {
		return nil, fmt.Errorf("configured MGR replication password exceeds MySQL group recovery maximum length of %d characters", mysqlMGRRecoveryPasswordMaxLength)
	}
	// Older Operator versions generated a 48-character replication secret.
	// MySQL 8.0 rejects that value specifically for the
	// group_replication_recovery channel. Preserve the existing random prefix
	// so the migration is deterministic, while the sidecar converges the
	// managed replication account to the same shortened value.
	return append([]byte(nil), password[:mysqlMGRRecoveryPasswordMaxLength]...), nil
}

// resolveMySQLRootPassword resolves the credential that the restored target
// must use while XtraBackup's source grant tables are present.  The default
// path is unchanged; AdoptSource is an explicit opt-in that reads only the
// source instance's operator-managed Secret and never carries credential
// material in a KDBInstance spec or task payload.
func resolveMySQLRootPassword(ctx stdcontext.Context, kubeClient client.Client, instance *v1.KDBInstance, configured string, existing []byte) ([]byte, error) {
	if instance == nil || instance.Spec.MySQL == nil || instance.Spec.MySQL.Restore == nil {
		return resolveMySQLCredentialValue(configured, existing)
	}
	restore := instance.Spec.MySQL.Restore
	mode := restore.CredentialMode
	if mode == "" {
		mode = v1.MySQLRestoreCredentialModeTarget
	}
	switch mode {
	case v1.MySQLRestoreCredentialModeTarget:
		return resolveMySQLCredentialValue(configured, existing)
	case v1.MySQLRestoreCredentialModeAdoptSource:
		if restore.SourceInstanceRef == nil || strings.TrimSpace(restore.SourceInstanceRef.Name) == "" {
			return nil, fmt.Errorf("mysql restore credential mode %q requires sourceInstanceRef", mode)
		}
		sourceName := strings.TrimSpace(restore.SourceInstanceRef.Name)
		if sourceName == instance.Name {
			return nil, fmt.Errorf("mysql restore sourceInstanceRef %q must differ from target instance", sourceName)
		}
		source := &v1.KDBInstance{}
		if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: sourceName}, source); err != nil {
			return nil, fmt.Errorf("read mysql restore source instance %q: %w", sourceName, err)
		}
		sourceCredential := &corev1.Secret{ObjectMeta: naming.MySQLCredentialSecret(source)}
		if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(sourceCredential), sourceCredential); err != nil {
			return nil, fmt.Errorf("read mysql restore source credential Secret %q: %w", sourceCredential.Name, err)
		}
		password := sourceCredential.Data[naming.MySQLRootPasswordSecretKey]
		if len(bytesTrimSpace(password)) == 0 {
			return nil, fmt.Errorf("mysql restore source credential Secret %q has no %q", sourceCredential.Name, naming.MySQLRootPasswordSecretKey)
		}
		return append([]byte(nil), password...), nil
	default:
		return nil, fmt.Errorf("unsupported mysql restore credential mode %q", mode)
	}
}

func bytesTrimSpace(value []byte) []byte {
	return []byte(strings.TrimSpace(string(value)))
}

// resolveReplications renders YAML items under replications: based on deploy arch.
func resolveReplications(rc *context.InstanceContext, instance *v1.KDBInstance, mode, replUser, replPasswordFile string) (string, error) {
	if instance == nil || instance.Spec.InstanceSet.Replicas == nil {
		return "", nil
	}
	replicas := int(*instance.Spec.InstanceSet.Replicas)
	if replicas <= 0 {
		return "", nil
	}

	masterPort := int(naming.KDBInstanceMasterPort(instance))
	serviceName := naming.InstancePodServiceName(instance.Name)
	lines := make([]string, 0, replicas*5)

	appendItem := func(podName, host, initRole string) {
		lines = append(lines,
			"  - pod_name: "+podName,
			"    port: "+strconv.Itoa(masterPort),
			"    host: "+host,
			"    repl_user: "+replUser,
			"    repl_password_file: "+replPasswordFile,
			"    init_role: "+initRole,
		)
	}

	plan, err := topology.ResolveInstancePlan(instance)
	if err != nil {
		return "", err
	}

	for i := 0; i < replicas; i++ {
		podName := naming.InstanceStatefulSetName(instance.Name, i) + "-0"
		host := naming.InstancePodHost(instance.Name, serviceName, instance.Namespace, i)
		if mode == config.HostResolveModeIP {
			pod := &corev1.Pod{}
			if err := rc.Client().Get(rc.Context(), client.ObjectKey{Namespace: instance.Namespace, Name: podName}, pod); err != nil {
				return "", err
			}
			if pod.Status.PodIP == "" {
				return "", fmt.Errorf("pod %s has empty podIP", podName)
			}
			host = pod.Status.PodIP
		}
		initRole := "replica"
		if i == plan.Primary {
			initRole = "master"
		}
		appendItem(podName, host, initRole)
	}

	if len(lines) == 0 {
		return "", nil
	}
	return strings.Join(lines, "\n"), nil
}

func resolveBackupTemplateConfig(values map[string]string) map[string]any {
	backend := backupConfigString(values, "backup.backend", "none")
	credentialRef := backupConfigString(values, "backup.credentialRef", "")
	accessKeyIDFile := backupConfigString(values, "backup.credentialAccessKeyIDFile", "")
	accessKeySecretFile := backupConfigString(values, "backup.credentialAccessKeySecretFile", "")
	securityTokenFile := backupConfigString(values, "backup.credentialSecurityTokenFile", "")
	sessionTokenFile := backupConfigString(values, "backup.credentialSessionTokenFile", "")
	if credentialRef != "" {
		if accessKeyIDFile == "" {
			accessKeyIDFile = naming.BackupAccessKeyIDPath
		}
		if accessKeySecretFile == "" {
			accessKeySecretFile = naming.BackupAccessKeySecretPath
		}
		if securityTokenFile == "" {
			securityTokenFile = naming.BackupSecurityTokenPath
		}
		if sessionTokenFile == "" {
			sessionTokenFile = naming.BackupSessionTokenPath
		}
	}
	return map[string]any{
		"BackupEnabled":             backupConfigBool(values, "backup.enabled", false),
		"BackupCrontab":             yamlQuote(backupConfigString(values, "backup.crontab", "")),
		"BackupDefaultMode":         yamlQuote(backupConfigString(values, "backup.defaultMode", "full")),
		"BackupBackend":             yamlQuote(backend),
		"BackupBaseDir":             yamlQuote(backupConfigString(values, "backup.baseDir", "/backup")),
		"BackupXtrabackupBin":       yamlQuote(backupConfigString(values, "backup.xtrabackupBin", "/usr/bin/xtrabackup")),
		"BackupXbstreamBin":         yamlQuote(backupConfigString(values, "backup.xbstreamBin", "/usr/bin/xbstream")),
		"BackupXbcloudBin":          yamlQuote(backupConfigString(values, "backup.xbcloudBin", "/usr/bin/xbcloud")),
		"BackupTimeoutSeconds":      backupConfigInt(values, "backup.timeoutSeconds", 1800),
		"BackupUploadRetries":       backupConfigInt(values, "backup.uploadRetries", 3),
		"BackupResumeMaxRetries":    backupConfigInt(values, "backup.resumeMaxRetries", 3),
		"BackupRetentionDays":       backupConfigInt(values, "backup.retentionDays", 7),
		"BackupCompress":            backupConfigBool(values, "backup.compress", false),
		"BackupCompression":         yamlQuote(backupConfigString(values, "backup.compression", "none")),
		"BackupConcurrency":         backupConfigInt(values, "backup.concurrency", 1),
		"BackupLocalEnabled":        backupConfigBool(values, "backup.local.enabled", backend == "local"),
		"BackupLocalRoot":           yamlQuote(backupConfigString(values, "backup.local.root", "/kdbdata/backup-artifacts")),
		"BackupOSSEnabled":          backupConfigBool(values, "backup.oss.enabled", backend == "oss"),
		"BackupOSSEndpoint":         yamlQuote(backupConfigString(values, "backup.oss.endpoint", "")),
		"BackupOSSBucket":           yamlQuote(backupConfigString(values, "backup.oss.bucket", "")),
		"BackupOSSPrefix":           yamlQuote(backupConfigString(values, "backup.oss.prefix", "")),
		"BackupOSSUseInsecure":      backupConfigBool(values, "backup.oss.useInsecure", false),
		"BackupS3Enabled":           backupConfigBool(values, "backup.s3.enabled", backend == "s3" || backend == "tos"),
		"BackupS3Endpoint":          yamlQuote(backupConfigString(values, "backup.s3.endpoint", "")),
		"BackupS3Region":            yamlQuote(backupConfigString(values, "backup.s3.region", "")),
		"BackupS3Bucket":            yamlQuote(backupConfigString(values, "backup.s3.bucket", "")),
		"BackupS3Prefix":            yamlQuote(backupConfigString(values, "backup.s3.prefix", "")),
		"BackupS3ForcePathStyle":    backupConfigBool(values, "backup.s3.forcePathStyle", false),
		"BackupS3UseSSL":            backupConfigBool(values, "backup.s3.useSSL", true),
		"BackupAccessKeyIDFile":     yamlQuote(accessKeyIDFile),
		"BackupAccessKeySecretFile": yamlQuote(accessKeySecretFile),
		"BackupSecurityTokenFile":   yamlQuote(securityTokenFile),
		"BackupSessionTokenFile":    yamlQuote(sessionTokenFile),
	}
}

func backupConfigString(values map[string]string, key, fallback string) string {
	if values == nil {
		return fallback
	}
	if value, ok := values[key]; ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func backupConfigBool(values map[string]string, key string, fallback bool) bool {
	value := backupConfigString(values, key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(strings.ToLower(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func backupConfigInt(values map[string]string, key string, fallback int) int {
	value := backupConfigString(values, key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func yamlQuote(value string) string {
	return strconv.Quote(value)
}
