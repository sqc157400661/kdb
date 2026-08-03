package pg

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func TestMOD07SecurityAndObservabilityContract(t *testing.T) {
	port := int32(5432)
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"}}
	instance.Spec.Engine = naming.PostgresEngine
	instance.Spec.EngineVersion = "17"
	instance.Spec.Port = &port
	instance.Spec.PostgreSQL = &v1.PostgreSQLSpec{Exporter: &v1.PostgreSQLExporterSpec{Enabled: true}}
	config, err := buildPatroniConfig(instance)
	if err != nil {
		t.Fatal(err)
	}
	pg := config["postgresql"].(map[string]interface{})
	params := pg["parameters"].(map[string]string)
	if params["password_encryption"] != "scram-sha-256" || params["ssl"] != "on" {
		t.Fatalf("secure PostgreSQL defaults missing: %#v", params)
	}
	hba := strings.Join(pg["pg_hba"].([]string), "\n")
	for _, expected := range []string{"hostssl replication replicator", "scram-sha-256 clientcert=verify-ca", "hostnossl all all 0.0.0.0/0 reject"} {
		if !strings.Contains(hba, expected) {
			t.Fatalf("HBA missing %q: %s", expected, hba)
		}
	}
	rest := config["restapi"].(map[string]interface{})
	if rest["verify_client"] != "required" || rest["certfile"] == nil {
		t.Fatalf("kdb-ha mTLS missing: %#v", rest)
	}
	policy := postgreSQLNetworkPolicy(instance, port)
	if policy.Kind != "NetworkPolicy" || len(policy.Spec.PolicyTypes) != 1 || len(policy.Spec.Ingress) != 4 {
		t.Fatalf("default-deny NetworkPolicy contract missing: %#v", policy.Spec)
	}
	controlPlaneRule := policy.Spec.Ingress[2]
	if len(controlPlaneRule.From) != 2 ||
		controlPlaneRule.From[0].PodSelector == nil ||
		controlPlaneRule.From[0].PodSelector.MatchExpressions[0].Key != "app.kubernetes.io/name" ||
		controlPlaneRule.From[1].PodSelector == nil ||
		controlPlaneRule.From[1].PodSelector.MatchExpressions[0].Key != "app" {
		t.Fatalf("canonical and legacy control-plane selectors must both be allowed: %#v", controlPlaneRule.From)
	}
	objects := postgreSQLMonitoringObjects(instance)
	if len(objects) != 2 {
		t.Fatalf("monitoring objects=%d", len(objects))
	}
	raw := objects[1].Object["spec"]
	text := strings.ToLower(strings.TrimSpace(toJSON(raw)))
	for _, alert := range []string{"exporterdown", "sqlprobedown", "hadown", "primarymissing", "splitbrainrisk", "replicationlaghigh", "connectionshigh", "longtransaction", "deadlocks", "freezeagehigh", "archivefailure", "backupfailure", "diskpressure"} {
		if !strings.Contains(text, alert) {
			t.Fatalf("default alert %s missing: %s", alert, text)
		}
	}
	for _, metric := range []string{"kdb_pg_activity", "kdb_pg_probe", "kdb_pg_replication", "kdb_pg_wal", "kdb_pg_vacuum", "kdb_pg_database", "kdb_pg_stat_statements"} {
		if !strings.Contains(postgreSQLExporterQueries, metric) {
			t.Fatalf("exporter query group %s missing", metric)
		}
	}
	queries := map[string]interface{}{}
	if err := yaml.Unmarshal([]byte(postgreSQLExporterQueries), &queries); err != nil {
		t.Fatalf("exporter queries are not valid YAML: %v", err)
	}
	if len(queries) < 8 {
		t.Fatalf("exporter query groups=%d, want at least 8", len(queries))
	}
}

func TestMOD09DRNetworkPolicyAllowsMTLSReplicationAcrossClusterBoundary(t *testing.T) {
	port := int32(5432)
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"}}
	instance.Spec.PostgreSQL = &v1.PostgreSQLSpec{DR: &v1.PostgreSQLDRSpec{Enabled: true}}
	policy := postgreSQLNetworkPolicy(instance, port)
	if len(policy.Spec.Ingress) != 5 {
		t.Fatalf("DR ingress rules=%d, want 5", len(policy.Spec.Ingress))
	}
	rule := policy.Spec.Ingress[4]
	if len(rule.From) != 1 || rule.From[0].IPBlock == nil || rule.From[0].IPBlock.CIDR != "0.0.0.0/0" || len(rule.Ports) != 1 || rule.Ports[0].Port.IntValue() != int(port) {
		t.Fatalf("cross-cluster replication ingress missing: %#v", rule)
	}
}

func TestMOD07GeneratedTLSBundleSupportsServerAndClientAuth(t *testing.T) {
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"}}
	bundle, err := generatePostgreSQLMTLSBundle(instance)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.CA) == 0 || len(bundle.ServerKey) == 0 || len(bundle.ClientKey) == 0 {
		t.Fatal("incomplete TLS bundle")
	}
	block, _ := pem.Decode(bundle.ServerCert)
	if block == nil {
		t.Fatal("invalid certificate PEM")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.ExtKeyUsage) != 1 || parsed.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth ||
		!strings.Contains(strings.Join(parsed.DNSNames, ","), "orders-rw.kdb.svc") {
		t.Fatalf("unexpected leaf certificate: %#v", parsed)
	}
	clientBlock, _ := pem.Decode(bundle.ClientCert)
	if clientBlock == nil {
		t.Fatal("invalid client certificate PEM")
	}
	client, err := x509.ParseCertificate(clientBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.ExtKeyUsage) != 1 || client.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth ||
		client.Subject.CommonName != "kdb-cluster-gateway" {
		t.Fatalf("unexpected client certificate: %#v", client)
	}
}

func toJSON(value interface{}) string { data, _ := json.Marshal(value); return string(data) }

func TestBuildPatroniConfigDefaults(t *testing.T) {
	port := int32(5432)
	instance := &v1.KDBInstance{}
	instance.Name = "demo-pg"
	instance.Namespace = "kdb"
	instance.Spec.Engine = naming.PostgresEngine
	instance.Spec.EngineVersion = "14"
	instance.Spec.Port = &port
	instance.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: util.Int32(1), MainContainer: shared.ContainerSpec{Image: "postgresql17:test"}, SidecarContainer: shared.ContainerSpec{Image: "postgresql-sidecar:test"}}

	conf, err := buildPatroniConfig(instance)
	if err != nil {
		t.Fatalf("buildPatroniConfig returned error: %v", err)
	}

	if got := conf["scope"]; got != "demo-pg" {
		t.Fatalf("unexpected scope: %v", got)
	}
	postgresql := conf["postgresql"].(map[string]interface{})
	if got := postgresql["data_dir"]; got != "/pgdata/pg14" {
		t.Fatalf("unexpected data_dir: %v", got)
	}
	if _, ok := postgresql["authentication"]; ok {
		t.Fatalf("postgresql authentication should be provided by Secret-backed PATRONI_* env")
	}
	if _, ok := postgresql["connect_address"]; ok {
		t.Fatalf("postgresql connect_address should be provided by PATRONI_POSTGRESQL_CONNECT_ADDRESS env")
	}
	if postgresql["use_pg_rewind"] != true {
		t.Fatalf("safe rejoin must enable pg_rewind: %#v", postgresql)
	}
	if postgresql["runtime_socket"] != "/var/run/kdb-ha/postgres-runtime.sock" || postgresql["tool_socket"] != "/var/run/kdb-ha/postgres-tools.sock" {
		t.Fatalf("split-container runtime sockets missing: %#v", postgresql)
	}
	parameters := postgresql["parameters"].(map[string]string)
	if parameters["wal_log_hints"] != "on" {
		t.Fatalf("safe rejoin must enable wal_log_hints: %#v", parameters)
	}
	if parameters["logging_collector"] != "on" || parameters["log_directory"] != naming.DatabaseLogRoot ||
		parameters["log_filename"] != "postgresql-%Y-%m-%d_%H%M%S.log" {
		t.Fatalf("canonical PostgreSQL file logging missing: %#v", parameters)
	}
	restapi := conf["restapi"].(map[string]interface{})
	if _, ok := restapi["connect_address"]; ok {
		t.Fatalf("restapi connect_address should be provided by PATRONI_RESTAPI_CONNECT_ADDRESS env")
	}
	kubernetes := conf["kubernetes"].(map[string]interface{})
	if got, exists := kubernetes["use_endpoints"]; exists {
		t.Fatalf("new PostgreSQL instances must not generate Endpoints DCS, got use_endpoints=%v", got)
	}
	dcs := conf["dcs"].(map[string]interface{})
	if dcs["type"] != "kubernetes" || dcs["kubernetes"] == nil {
		t.Fatalf("native kdb-ha kubernetes DCS config missing: %#v", dcs)
	}
}

func TestBuildPatroniConfigStrongHAPreservesStrictQuorum(t *testing.T) {
	port := int32(5432)
	quorum := int32(1)
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "strong", Namespace: "kdb"}}
	instance.Spec.Engine = naming.PostgresEngine
	instance.Spec.EngineVersion = "17"
	instance.Spec.Port = &port
	instance.Spec.InstanceSet.Replicas = util.Int32(3)
	instance.Spec.PostgreSQL = &v1.PostgreSQLSpec{HA: &v1.PostgreSQLHASpec{
		Profile: "strong-ha", SynchronousMode: true, SynchronousModeStrict: true, SynchronousNodeCount: &quorum,
	}}
	conf, err := buildPatroniConfig(instance)
	if err != nil {
		t.Fatal(err)
	}
	ha := conf["ha"].(map[string]interface{})
	if ha["synchronous_mode"] != true || ha["synchronous_mode_strict"] != true || ha["synchronous_node_count"] != int32(1) {
		t.Fatalf("strong-ha config lost strict quorum: %#v", ha)
	}
}

func TestLegacySameImageConfigDoesNotRequireRuntimeSockets(t *testing.T) {
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "kdb"}, Spec: v1.KDBInstanceSpec{
		Engine: naming.PostgresEngine, EngineVersion: "17",
		InstanceSet: shared.InstanceSetSpec{MainContainer: shared.ContainerSpec{Image: "postgresql17:legacy"}, SidecarContainer: shared.ContainerSpec{Image: "postgresql17:legacy"}},
	}}
	config, err := buildPatroniConfig(instance)
	if err != nil {
		t.Fatal(err)
	}
	postgresql := config["postgresql"].(map[string]interface{})
	if postgresql["runtime_socket"] != nil || postgresql["tool_socket"] != nil {
		t.Fatalf("legacy runtime unexpectedly requires split-image sockets: %#v", postgresql)
	}
}

func TestBuildPatroniConfigRendersIndependentEtcd3DRStandby(t *testing.T) {
	port := int32(5432)
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders-b", Namespace: "kdb"}}
	instance.Spec.Engine, instance.Spec.EngineVersion, instance.Spec.Port = naming.PostgresEngine, "17", &port
	instance.Spec.PostgreSQL = &v1.PostgreSQLSpec{DR: &v1.PostgreSQLDRSpec{
		Enabled: true, Role: "standby", ClusterID: "cluster-b", PeerClusterID: "cluster-a", Scope: "orders-prod", ManualPromotionOnly: true,
		Etcd3:     v1.PostgreSQLDREtcd3Spec{Endpoints: []string{"https://etcd.example:2379"}, Prefix: "/kdb/dr", SecretRef: corev1.LocalObjectReference{Name: "orders-etcd"}},
		WALSource: v1.PostgreSQLDRWALSource{PrimaryConnInfo: "host=orders-a port=5432 sslmode=verify-full", ArchivePrefix: "s3://backups/orders"},
	}}
	config, err := buildPatroniConfig(instance)
	if err != nil {
		t.Fatal(err)
	}
	if config["dcs"].(map[string]interface{})["type"] != "kubernetes" {
		t.Fatal("member DCS must remain local Kubernetes")
	}
	dr := config["dr"].(map[string]interface{})
	if dr["role"] != "standby" || dr["manual_promotion_only"] != true || dr["dcs"].(map[string]interface{})["type"] != "etcd3" {
		t.Fatalf("invalid DR config: %#v", dr)
	}
	if config["ha"].(map[string]interface{})["standby_cluster"].(map[string]interface{})["primary_conninfo"] == "" {
		t.Fatal("standby WAL source was not rendered")
	}
}

func TestPostgreSQLConditionsTwoZonesAreDegradedButAvailable(t *testing.T) {
	replicas := int32(3)
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Generation: 7}}
	instance.Spec.InstanceSet.Replicas = &replicas
	status := &v1.PostgreSQLStatus{Primary: "pg-0", Replicas: []string{"pg-1", "pg-2"}}
	setPostgreSQLConditions(instance, status, 3, 2)
	conditions := map[string]metav1.Condition{}
	for _, condition := range instance.Status.Conditions {
		conditions[condition.Type] = condition
	}
	if conditions[v1.PostgreSQLAvailable].Status != metav1.ConditionTrue || conditions[v1.PostgreSQLReplicationHealthy].Status != metav1.ConditionTrue {
		t.Fatalf("healthy cluster should be available: %#v", conditions)
	}
	if conditions[v1.PostgreSQLTopologyDegraded].Status != metav1.ConditionTrue {
		t.Fatalf("three replicas spread over only two zones must report degraded topology: %#v", conditions)
	}
}

func TestBuildPatroniConfigDefaultsEngineVersion(t *testing.T) {
	port := int32(5432)
	instance := &v1.KDBInstance{}
	instance.Name = "demo-pg"
	instance.Namespace = "kdb"
	instance.Spec.Engine = naming.PostgresEngine
	instance.Spec.Port = &port
	instance.Spec.InstanceSet = shared.InstanceSetSpec{Replicas: util.Int32(1)}

	conf, err := buildPatroniConfig(instance)
	if err != nil {
		t.Fatalf("buildPatroniConfig returned error: %v", err)
	}

	postgresql := conf["postgresql"].(map[string]interface{})
	if got := postgresql["data_dir"]; got != "/pgdata/pg14" {
		t.Fatalf("unexpected default data_dir: %v", got)
	}
	if got := postgresql["bin_dir"]; got != "/usr/lib/postgresql/14/bin" {
		t.Fatalf("unexpected default bin_dir: %v", got)
	}
}

func TestBuildPatroniConfigRejectsEtcdUntilImplemented(t *testing.T) {
	port := int32(5432)
	instance := &v1.KDBInstance{}
	instance.Name = "demo-pg"
	instance.Namespace = "kdb"
	instance.Spec.Engine = naming.PostgresEngine
	instance.Spec.EngineVersion = "14"
	instance.Spec.Port = &port
	instance.Spec.PostgreSQL = &v1.PostgreSQLSpec{
		Patroni: &v1.PostgreSQLPatroniSpec{DCS: v1.PostgreSQLDCSEtcd},
	}

	if _, err := buildPatroniConfig(instance); err == nil {
		t.Fatalf("expected unsupported etcd DCS error")
	}
}

func TestBootstrapParametersAreRenderedOnceThenIgnored(t *testing.T) {
	port := int32(5432)
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"}}
	instance.Spec.Engine = naming.PostgresEngine
	instance.Spec.EngineVersion = "17"
	instance.Spec.Port = &port
	instance.Spec.InstanceSet.Replicas = util.Int32(3)
	instance.Spec.PostgreSQL = &v1.PostgreSQLSpec{Parameters: map[string]string{"work_mem": "16MB"}}
	before, err := buildPatroniConfig(instance)
	if err != nil {
		t.Fatal(err)
	}
	if before["postgresql"].(map[string]interface{})["parameters"].(map[string]string)["work_mem"] != "16MB" {
		t.Fatal("bootstrap parameter missing")
	}
	hash := postgreSQLParametersHash(instance.Spec.PostgreSQL.Parameters)
	instance.Status.PostgreSQL = &v1.PostgreSQLStatus{Ready: true, BootstrapParameters: &v1.PostgreSQLBootstrapParametersStatus{InputHash: hash, Revision: 2, State: "Applied"}}
	instance.Spec.PostgreSQL.Parameters["work_mem"] = "64MB"
	after, err := buildPatroniConfig(instance)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after["postgresql"].(map[string]interface{})["parameters"].(map[string]string)["work_mem"]; ok {
		t.Fatal("Ready reconcile must not render changed bootstrap parameters")
	}
	if instance.Status.PostgreSQL.BootstrapParameters.InputHash != hash {
		t.Fatal("recorded bootstrap hash changed")
	}
}

func TestBuildPGBackRestS3ConfigHasContinuousArchivePolicy(t *testing.T) {
	verify := false
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "kdb"}, Spec: v1.KDBInstanceSpec{EngineVersion: "17", InstanceSet: shared.InstanceSetSpec{MainContainer: shared.ContainerSpec{Image: "postgresql17:test"}, SidecarContainer: shared.ContainerSpec{Image: "postgresql-sidecar:test"}}, PostgreSQL: &v1.PostgreSQLSpec{Backups: &v1.PostgreSQLBackupSpec{PGBackRest: &v1.PostgreSQLPGBackRestSpec{Enabled: true, RepoType: "s3", RepoPath: "/kdb/orders", S3Bucket: "backups", S3Endpoint: "http://minio:9000", S3Region: "us-east-1", S3URIStyle: "path", S3TLSVerify: &verify}}}}}
	config := buildPGBackRestConfig(instance)
	for _, expected := range []string{"repo1-type=s3", "repo1-s3-bucket=backups", "repo1-s3-endpoint=minio", "repo1-storage-port=9000", "repo1-s3-uri-style=path", "repo1-storage-verify-tls=n", "repo1-retention-full=3", "archive-async=y", "pg1-socket-path=/tmp/postgres"} {
		if !strings.Contains(config, expected) {
			t.Fatalf("pgBackRest config missing %q:\n%s", expected, config)
		}
	}
	if pgBackRestRetentionDays(instance) != 14 || pgBackRestFullSchedule(instance) != "0 1 * * 0" || pgBackRestDifferentialSchedule(instance) != "0 1 * * 1-6" {
		t.Fatalf("unexpected default policy")
	}
	patroni, err := buildPatroniConfig(instance)
	if err != nil {
		t.Fatal(err)
	}
	archiveCommand := patroni["postgresql"].(map[string]interface{})["parameters"].(map[string]string)["archive_command"]
	if strings.Contains(archiveCommand, "stanza-create") || !strings.Contains(archiveCommand, "pgbackrest-stanza.ready") || !strings.Contains(archiveCommand, "kdb-pg-tool") || !strings.Contains(archiveCommand, "archive-push %p") {
		t.Fatalf("archive command does not order stanza creation before WAL push: %s", archiveCommand)
	}
}

func TestBuildPGBackRestLocalConfigUsesNativePosixType(t *testing.T) {
	instance := &v1.KDBInstance{
		Spec: v1.KDBInstanceSpec{
			EngineVersion: "17",
			PostgreSQL: &v1.PostgreSQLSpec{
				Backups: &v1.PostgreSQLBackupSpec{
					PGBackRest: &v1.PostgreSQLPGBackRestSpec{
						Enabled:  true,
						RepoType: "local",
						RepoPath: "/kdb/orders",
					},
				},
			},
		},
	}
	config := buildPGBackRestConfig(instance)
	if !strings.Contains(config, "repo1-type=posix") {
		t.Fatalf("local repository must map to pgBackRest posix:\n%s", config)
	}
	if strings.Contains(config, "repo1-type=local") {
		t.Fatalf("platform repository type leaked into pgBackRest config:\n%s", config)
	}
}

func TestPostgreSQLBackupCronMatchesUTCMinute(t *testing.T) {
	sundayFull := time.Date(2026, time.July, 19, 1, 0, 0, 0, time.UTC)
	matches, err := postgreSQLCronMatches("0 1 * * 0", sundayFull)
	if err != nil {
		t.Fatal(err)
	}
	if !matches {
		t.Fatal("weekly full schedule should match its UTC minute")
	}
	matches, err = postgreSQLCronMatches("0 1 * * 1-6", sundayFull)
	if err != nil {
		t.Fatal(err)
	}
	if matches {
		t.Fatal("weekday differential schedule must not match Sunday")
	}
	if _, err = postgreSQLCronMatches("invalid", sundayFull); err == nil {
		t.Fatal("invalid cron expression must be rejected")
	}
}
