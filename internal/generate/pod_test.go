package generate

import (
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
)

func TestMySQLExporterEnabled(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Spec.Engine = naming.MySQLEngine
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
	if len(container.Env) != 2 {
		t.Fatalf("expected merged env length 2, got %d", len(container.Env))
	}
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].Name != "tmp" {
		t.Fatalf("expected only tmp mount, got %#v", container.VolumeMounts)
	}
}
