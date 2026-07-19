package postgresqllifecycle

import (
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"k8s.io/apimachinery/pkg/api/resource"
)

func lifecycleInstance(replicas int32, size string) *v1.KDBInstance {
	return &v1.KDBInstance{Spec: v1.KDBInstanceSpec{Engine: "postgresql", EngineVersion: "17", InstanceSet: shared.InstanceSetSpec{Replicas: &replicas, DataVolumeClaimSpec: shared.PVCSpec{Size: resource.MustParse(size)}}, PostgreSQL: &v1.PostgreSQLSpec{HA: &v1.PostgreSQLHASpec{Profile: "standard-ha"}}}}
}

func TestRejectsUnsafeScaleDownAndPVCShrink(t *testing.T) {
	old := lifecycleInstance(3, "10Gi")
	old.Status.PostgreSQL = &v1.PostgreSQLStatus{Primary: "pg0"}
	tooSmall := lifecycleInstance(2, "10Gi")
	if err := ValidateUpdate(old, tooSmall); err == nil || !strings.Contains(err.Error(), "at least 3") {
		t.Fatalf("unsafe scale down error=%v", err)
	}
	shrunk := lifecycleInstance(3, "9Gi")
	if err := ValidateUpdate(old, shrunk); err == nil || !strings.Contains(err.Error(), "cannot decrease") {
		t.Fatalf("PVC shrink error=%v", err)
	}
}

func TestMajorUpgradePreflightAndCustomImageAdmission(t *testing.T) {
	instance := lifecycleInstance(3, "10Gi")
	instance.Spec.PostgreSQL.Lifecycle = &v1.PostgreSQLLifecycleSpec{Upgrade: &v1.PostgreSQLUpgradeSpec{OperationID: "u1", Type: "Major", TargetMajorVersion: "18", TargetImage: "registry/pg18@sha256:" + strings.Repeat("a", 64)}}
	if err := ValidateUpdate(nil, instance); err == nil || !strings.Contains(err.Error(), "maintenance window") {
		t.Fatalf("major preflight error=%v", err)
	}
	upgrade := instance.Spec.PostgreSQL.Lifecycle.Upgrade
	upgrade.MaintenanceWindow, upgrade.RecoverableBackupRef, upgrade.ExtensionsCompatible, upgrade.AcceptIrreversible = "mw-1", "backup-1", true, true
	instance.Spec.PostgreSQL.Lifecycle.Extensions = []v1.PostgreSQLExtensionSpec{{Name: "private-ext", CustomImage: &v1.PostgreSQLCustomExtensionImage{Image: "registry/ext:latest", ScanStatus: "Passed"}}}
	if err := ValidateUpdate(nil, instance); err == nil || !strings.Contains(err.Error(), "immutable digest") {
		t.Fatalf("unsigned image error=%v", err)
	}
	custom := instance.Spec.PostgreSQL.Lifecycle.Extensions[0].CustomImage
	custom.Image = "registry/ext@sha256:" + strings.Repeat("b", 64)
	custom.SignatureVerified, custom.AttestationRef = true, "rekor://entry/1"
	if err := ValidateUpdate(nil, instance); err != nil {
		t.Fatalf("valid major upgrade rejected: %v", err)
	}
	instance.Spec.EngineVersion = "18"
	instance.Status.PostgreSQL = &v1.PostgreSQLStatus{Lifecycle: &v1.PostgreSQLLifecycleStatus{OperationID: "u1", Phase: "RebuildingReplicas"}}
	if err := ValidateUpdate(nil, instance); err != nil {
		t.Fatalf("persisted major target rejected while rebuilding replicas: %v", err)
	}
}
