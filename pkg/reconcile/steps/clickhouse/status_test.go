package clickhouse

import (
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestClickHouseLocalBackupRunnerIsConfigured(t *testing.T) {
	enabled := true
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "ch-local"}, Spec: v1.KDBInstanceSpec{Engine: "clickhouse", ClickHouse: &v1.ClickHouseSpec{Backup: &v1.ClickHouseBackupSpec{Enabled: &enabled}}}}
	if !clickHouseBackupRunnerEnabled(instance) {
		t.Fatal("local backup runner should be enabled without objectStorageRef")
	}
}
