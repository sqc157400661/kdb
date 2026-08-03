package generate

import (
	"reflect"
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
)

func TestMySQLDatabaseLogInitUsesDataPVC(t *testing.T) {
	container := mysqlDatabaseLogInitContainer(shared.InstanceSetSpec{MainContainer: shared.ContainerSpec{Image: "mysql:test"}})
	if container.Name != "database-log-init" || container.Image != "mysql:test" {
		t.Fatalf("unexpected init container identity: %#v", container)
	}
	if !strings.Contains(strings.Join(container.Command, " "), naming.DatabaseLogRoot) {
		t.Fatalf("init command does not create canonical log root: %#v", container.Command)
	}
	wantMount := naming.DataVolumeMount()
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].Name != wantMount.Name ||
		container.VolumeMounts[0].MountPath != wantMount.MountPath {
		t.Fatalf("init container does not mount the data PVC: %#v", container.VolumeMounts)
	}
}

func TestInstanceRuntimeCommandsPreserveNonPostgreSQLComposition(t *testing.T) {
	for _, engine := range []string{naming.MySQLEngine, naming.ClickHouseEngine} {
		main, sidecar, args := instanceRuntimeCommands(engine)
		if !reflect.DeepEqual(main, []string{"/bin/bash", "-c", "/kdb/bin/run_supervisor.sh"}) ||
			!reflect.DeepEqual(sidecar, []string{"/kdb/bin/start.sh"}) || len(args) != 0 {
			t.Fatalf("engine %s runtime changed: main=%v sidecar=%v args=%v", engine, main, sidecar, args)
		}
	}
}

func TestInstanceRuntimeCommandsUseKDBHAForPostgreSQL(t *testing.T) {
	main, sidecar, args := instanceRuntimeCommands(naming.PostgresEngine)
	if !reflect.DeepEqual(main, []string{"kdb-pg-runtime"}) || !reflect.DeepEqual(sidecar, []string{"kdb-ha"}) ||
		!reflect.DeepEqual(args, []string{"/etc/patroni/patroni.yaml"}) {
		t.Fatalf("unexpected PostgreSQL runtime: main=%v sidecar=%v args=%v", main, sidecar, args)
	}
}

func TestParameterReportSecretProjection(t *testing.T) {
	projection := parameterReportSecretProjection()
	if projection.Secret == nil {
		t.Fatalf("expected secret projection")
	}
	if projection.Secret.Name != naming.GlobalConfigSecret {
		t.Fatalf("unexpected secret name: %s", projection.Secret.Name)
	}
	if projection.Secret.Optional == nil || !*projection.Secret.Optional {
		t.Fatalf("parameter report secret projection should be optional")
	}
	want := map[string]string{
		naming.ParameterReportHostSecretKey:    naming.ParameterReportHostSecretKey,
		naming.ParameterReportTokenSecretKey:   naming.ParameterReportTokenSecretKey,
		naming.ParameterReportCatalogSecretKey: naming.ParameterReportCatalogSecretKey,
	}
	if len(projection.Secret.Items) != len(want) {
		t.Fatalf("unexpected projection items: %#v", projection.Secret.Items)
	}
	for _, item := range projection.Secret.Items {
		if want[item.Key] != item.Path {
			t.Fatalf("unexpected key/path: %#v", item)
		}
		delete(want, item.Key)
	}
	if len(want) != 0 {
		t.Fatalf("missing projection items: %#v", want)
	}
}

func TestMySQLExporterEnabled(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Spec.Engine = "MySQL"
	instance.Spec.MySQL = &v1.MySQLSpec{
		Exporter: &v1.MySQLExporterSpec{Enabled: true},
	}
	if !mysqlExporterEnabled(instance) {
		t.Fatalf("expected mysql exporter to be enabled")
	}
	instance.Spec.MySQL.Exporter.Enabled = false
	if mysqlExporterEnabled(instance) {
		t.Fatalf("expected mysql exporter to be disabled")
	}
}

func TestMySQLExporterContainer(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Spec.MySQL = &v1.MySQLSpec{
		Exporter: &v1.MySQLExporterSpec{
			Enabled: true,
			Image:   "kdbdeveloper/mysql-exporter:v0.0.1",
			Env: []corev1.EnvVar{{
				Name:  "MYSQL_EXPORTER_USER",
				Value: "exporter",
			}},
		},
	}
	instanceSet := shared.InstanceSetSpec{
		MonitorContainer: shared.ContainerSpec{
			Image: "fallback-exporter:latest",
			Env: []corev1.EnvVar{{
				Name:  "WEB_LISTEN_ADDRESS",
				Value: ":9104",
			}},
		},
	}
	container := mysqlExporterContainer(instance, instanceSet, []corev1.VolumeMount{{
		Name:      "tmp",
		MountPath: "/tmp",
	}})
	if container.Name != naming.ContainerMySQLExporter {
		t.Fatalf("unexpected container name: %s", container.Name)
	}
	if container.Image != "kdbdeveloper/mysql-exporter:v0.0.1" {
		t.Fatalf("unexpected exporter image: %s", container.Image)
	}
	if len(container.Ports) != 1 || container.Ports[0].Name != naming.PortMySQLMetrics || container.Ports[0].ContainerPort != 9104 {
		t.Fatalf("unexpected exporter ports: %#v", container.Ports)
	}
	if len(container.Env) != 4 {
		t.Fatalf("expected merged/default env length 4, got %d", len(container.Env))
	}
	env := map[string]corev1.EnvVar{}
	for _, item := range container.Env {
		env[item.Name] = item
	}
	if env["MYSQL_EXPORTER_USER"].Value != "exporter" {
		t.Fatalf("explicit exporter user was not preserved: %#v", env["MYSQL_EXPORTER_USER"])
	}
	password := env["MYSQL_EXPORTER_PASSWORD"]
	if password.ValueFrom == nil || password.ValueFrom.SecretKeyRef == nil ||
		password.ValueFrom.SecretKeyRef.Name != instance.Name+"-mysql-credential" ||
		password.ValueFrom.SecretKeyRef.Key != naming.MySQLRootPasswordSecretKey {
		t.Fatalf("exporter password is not sourced from the operator credential Secret: %#v", password)
	}
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].Name != "tmp" {
		t.Fatalf("expected only tmp mount, got %#v", container.VolumeMounts)
	}
}

func TestMySQLExporterContainerReplacesLegacyEmptyPassword(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Name = "mysql-legacy"
	instance.Spec.MySQL = &v1.MySQLSpec{Exporter: &v1.MySQLExporterSpec{
		Enabled: true,
		Env: []corev1.EnvVar{
			{Name: "MYSQL_EXPORTER_USER", Value: "_monitor_user"},
			{Name: "MYSQL_EXPORTER_PASSWORD", Value: ""},
			{Name: "MYSQL_EXPORTER_HOST", Value: "127.0.0.1"},
		},
	}}
	container := mysqlExporterContainer(instance, shared.InstanceSetSpec{}, nil)
	for _, item := range container.Env {
		if item.Name != "MYSQL_EXPORTER_PASSWORD" {
			continue
		}
		if item.ValueFrom == nil || item.ValueFrom.SecretKeyRef == nil ||
			item.ValueFrom.SecretKeyRef.Name != "mysql-legacy-mysql-credential" {
			t.Fatalf("legacy empty password was not migrated to SecretKeyRef: %#v", item)
		}
		return
	}
	t.Fatal("MYSQL_EXPORTER_PASSWORD was not rendered")
}
