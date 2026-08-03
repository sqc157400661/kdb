package mysql

import (
	stdcontext "context"
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/config"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/internal/topology"
	"github.com/sqc157400661/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func int32ptr(v int32) *int32 { return &v }

func TestResolveMGRSeeds(t *testing.T) {
	ins := &v1.KDBInstance{}
	ins.Name = "demo"
	ins.Namespace = "ns"
	ins.Spec.DeployArch = naming.MySQLMGRDeployArch
	ins.Spec.Leader = v1.HostInfo{Port: 3306}
	ins.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: int32ptr(3)}

	seeds := topology.BuildMGRSeeds(ins, topology.DefaultMGRGroupPort)
	want := "demo0-0.demo.ns.svc.cluster.local:33061,demo1-0.demo.ns.svc.cluster.local:33061,demo2-0.demo.ns.svc.cluster.local:33061"
	if seeds != want {
		t.Fatalf("unexpected seeds: %s", seeds)
	}
}

func TestInstanceConfigTemplateIncludesParameterReportPaths(t *testing.T) {
	report := (&config.GlobalConfig{}).GetParameterReportConfig()
	data := map[string]any{
		"RootUser":                       "root",
		"RootPasswordFile":               naming.MySQLRootPasswordPath,
		"ReplUser":                       "repl",
		"ReplPasswordFile":               naming.MySQLReplicationPasswordPath,
		"CurrentVersion":                 "1",
		"UpdateVersion":                  "1",
		"Replications":                   "",
		"DeployArch":                     naming.MySQLSingleDeployArch,
		"MGREnabled":                     false,
		"MGRMode":                        "",
		"MGRGroupName":                   "",
		"MGRLocalAddress":                "",
		"MGRSeeds":                       "",
		"MGRBootstrap":                   false,
		"MGRBootstrapOrdinal":            0,
		"MGRGroupPort":                   0,
		"ParameterReportEnabled":         report.Enabled,
		"ParameterReportHostFile":        report.HostFile,
		"ParameterReportTokenFile":       report.TokenFile,
		"ParameterReportCatalogFile":     report.CatalogFile,
		"ParameterReportIntervalSeconds": report.IntervalSeconds,
		"ParameterReportTimeoutSeconds":  report.TimeoutSeconds,
	}
	for key, value := range resolveBackupTemplateConfig(nil) {
		data[key] = value
	}
	got, err := util.SafeTemplateFill(config.InstanceConfigTmpl, data)
	if err != nil {
		t.Fatalf("render instance config template: %v", err)
	}
	for _, want := range []string{
		"parameter_report:",
		"enabled: true",
		"host_file: " + naming.ParameterReportHostPath,
		"token_file: " + naming.ParameterReportTokenPath,
		"catalog_file: " + naming.ParameterReportCatalogPath,
		"interval_seconds: 60",
		"timeout_seconds: 10",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, got)
		}
	}
}

func TestResolveBackupTemplateConfigLocalStorage(t *testing.T) {
	data := resolveBackupTemplateConfig(map[string]string{
		"backup.enabled":       "true",
		"backup.backend":       "local",
		"backup.crontab":       "0 0 2 * * *",
		"backup.local.root":    "/kdbdata/local-backups",
		"backup.retentionDays": "14",
	})

	if data["BackupEnabled"] != true {
		t.Fatalf("BackupEnabled = %v, want true", data["BackupEnabled"])
	}
	if data["BackupBackend"] != `"local"` {
		t.Fatalf("BackupBackend = %v, want quoted local", data["BackupBackend"])
	}
	if data["BackupCrontab"] != `"0 0 2 * * *"` {
		t.Fatalf("BackupCrontab = %v, want quoted cron", data["BackupCrontab"])
	}
	if data["BackupLocalRoot"] != `"/kdbdata/local-backups"` {
		t.Fatalf("BackupLocalRoot = %v, want local root", data["BackupLocalRoot"])
	}
	if data["BackupRetentionDays"] != 14 {
		t.Fatalf("BackupRetentionDays = %v, want 14", data["BackupRetentionDays"])
	}
}

func TestResolveMySQLCredentialValueGeneratesAndPreservesFallback(t *testing.T) {
	generated, err := resolveMySQLCredentialValue("", nil)
	if err != nil {
		t.Fatalf("generate empty credential: %v", err)
	}
	if len(generated) != 48 {
		t.Fatalf("generated credential length = %d, want 48 hex characters", len(generated))
	}
	preserved, err := resolveMySQLCredentialValue("", generated)
	if err != nil {
		t.Fatalf("preserve generated credential: %v", err)
	}
	if string(preserved) != string(generated) {
		t.Fatal("empty global configuration rotated an existing generated credential")
	}
	configured, err := resolveMySQLCredentialValue(" configured-secret ", generated)
	if err != nil {
		t.Fatalf("use configured credential: %v", err)
	}
	if string(configured) != " configured-secret " {
		t.Fatalf("configured credential = %q", configured)
	}
}

func TestResolveMySQLReplicationPasswordEnforcesMGRRecoveryLimit(t *testing.T) {
	instance := &v1.KDBInstance{Spec: v1.KDBInstanceSpec{DeployArch: "MGR"}}
	existing := []byte("0123456789abcdef0123456789abcdef0123456789abcdef")

	resolved, err := resolveMySQLReplicationPassword(instance, "", existing)
	if err != nil {
		t.Fatalf("migrate generated MGR replication credential: %v", err)
	}
	if got, want := string(resolved), string(existing[:mysqlMGRRecoveryPasswordMaxLength]); got != want {
		t.Fatalf("resolved MGR replication credential = %q, want deterministic prefix %q", got, want)
	}

	if _, err := resolveMySQLReplicationPassword(instance, string(existing), nil); err == nil || !strings.Contains(err.Error(), "maximum length of 32") {
		t.Fatalf("configured overlength MGR replication credential error = %v, want maximum length validation", err)
	}

	nonMGR := &v1.KDBInstance{Spec: v1.KDBInstanceSpec{DeployArch: "Master-Replica"}}
	resolved, err = resolveMySQLReplicationPassword(nonMGR, "", existing)
	if err != nil {
		t.Fatalf("preserve non-MGR replication credential: %v", err)
	}
	if string(resolved) != string(existing) {
		t.Fatal("non-MGR replication credential was unexpectedly shortened")
	}
}

func TestResolveMySQLRootPasswordAdoptsSourceCredential(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("add kdb scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	source := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "mysql-source", Namespace: "kdb"}}
	target := &v1.KDBInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "mysql-target", Namespace: "kdb"},
		Spec: v1.KDBInstanceSpec{MySQL: &v1.MySQLSpec{Restore: &v1.MySQLRestoreSpec{
			CredentialMode:    v1.MySQLRestoreCredentialModeAdoptSource,
			SourceInstanceRef: &corev1.LocalObjectReference{Name: source.Name},
		}}},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		source,
		&corev1.Secret{ObjectMeta: naming.MySQLCredentialSecret(source), Data: map[string][]byte{
			naming.MySQLRootPasswordSecretKey: []byte("source-secret"),
		}},
	).Build()
	got, err := resolveMySQLRootPassword(stdcontext.Background(), client, target, "target-secret", []byte("old-target-secret"))
	if err != nil {
		t.Fatalf("resolveMySQLRootPassword() error = %v", err)
	}
	if string(got) != "source-secret" {
		t.Fatalf("root password = %q, want source credential", got)
	}
}

func TestResolveMySQLRootPasswordTargetModePreservesLegacyResolution(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("add kdb scheme: %v", err)
	}
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	target := &v1.KDBInstance{Spec: v1.KDBInstanceSpec{MySQL: &v1.MySQLSpec{Restore: &v1.MySQLRestoreSpec{
		CredentialMode: v1.MySQLRestoreCredentialModeTarget,
	}}}}
	got, err := resolveMySQLRootPassword(stdcontext.Background(), client, target, "target-secret", []byte("old-target-secret"))
	if err != nil {
		t.Fatalf("resolveMySQLRootPassword() error = %v", err)
	}
	if string(got) != "target-secret" {
		t.Fatalf("root password = %q, want configured target credential", got)
	}

	target.Spec.MySQL.Restore.CredentialMode = v1.MySQLRestoreCredentialModeAdoptSource
	if _, err = resolveMySQLRootPassword(stdcontext.Background(), client, target, "", nil); err == nil || !strings.Contains(err.Error(), "sourceInstanceRef") {
		t.Fatalf("missing sourceInstanceRef error = %v", err)
	}
}
