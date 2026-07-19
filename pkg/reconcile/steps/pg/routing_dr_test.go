package pg

import (
	"testing"
	"time"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
)

func TestProjectPostgreSQLDRStatusPreservesDurableDrill(t *testing.T) {
	drill := &v1.PostgreSQLDRDrillStatus{OperationID: "promote-1", PreviousTerm: 1, CurrentTerm: 2, RTOSeconds: 70}
	status := projectPostgreSQLDRStatusPreservingDrill(&postgreSQLNativeDRStatus{
		Enabled: true, ClusterID: "cluster-b", RuntimeRole: "active", ActiveClusterID: "cluster-b", Term: 2,
	}, &v1.PostgreSQLDRStatus{LastDrill: drill})
	if status.LastDrill != drill || status.LastDrill.OperationID != "promote-1" || status.Term != 2 {
		t.Fatalf("durable drill was not preserved: %#v", status)
	}
}

func TestProjectPostgreSQLDRStatusComputesObservedRPO(t *testing.T) {
	now := time.Now().UTC()
	status := projectPostgreSQLDRStatus(&postgreSQLNativeDRStatus{
		Enabled: true, ClusterID: "cluster-b", PeerClusterID: "cluster-a", RuntimeRole: "standby", ActiveClusterID: "cluster-a", Term: 7, Connected: true, ManualPromotionOnly: true,
		ClusterHeartbeats: map[string]postgreSQLNativeDRHeartbeat{
			"cluster-a": {ClusterID: "cluster-a", Role: "active", LSN: "1/00000100", ObservedAt: now},
			"cluster-b": {ClusterID: "cluster-b", Role: "standby", LSN: "1/00000080", ObservedAt: now.Add(-3 * time.Second)},
		},
	})
	if status.RPOBytes != 128 || status.RPOSeconds != 3 || status.RuntimeRole != "standby" {
		t.Fatalf("unexpected DR projection: %#v", status)
	}
}

func TestParsePostgreSQLLSNRejectsInvalidValue(t *testing.T) {
	if value, ok := parsePostgreSQLLSN("2/10"); !ok || value != 2<<32|16 {
		t.Fatalf("valid LSN=%d ok=%v", value, ok)
	}
	if _, ok := parsePostgreSQLLSN("invalid"); ok {
		t.Fatal("invalid LSN accepted")
	}
}
