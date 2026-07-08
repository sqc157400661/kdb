package naming

import (
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
)

func TestClickHouseEngineRecognition(t *testing.T) {
	cases := []struct {
		name         string
		engine       string
		clickhouse   bool
		mysql        bool
		postgresql   bool
		defaultPort  int32
		clickHTTP    int32
		clickNative  int32
		clickHTTPS   int32
		clickNativeS int32
	}{
		{name: "mysql", engine: "mysql", mysql: true, defaultPort: 3306},
		{name: "postgresql", engine: "postgresql", postgresql: true, defaultPort: 5432},
		{name: "pg", engine: "pg", postgresql: true, defaultPort: 5432},
		{name: "clickhouse", engine: "clickhouse", clickhouse: true, defaultPort: 8123, clickHTTP: 8123, clickNative: 9000, clickHTTPS: 8443, clickNativeS: 9440},
		{name: "unknown", engine: "unknown", defaultPort: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			instance := &v1.KDBInstance{Spec: v1.KDBInstanceSpec{Engine: c.engine}}
			if got := IsClickHouseEngine(instance); got != c.clickhouse {
				t.Fatalf("IsClickHouseEngine() = %v, want %v", got, c.clickhouse)
			}
			if got := IsMySQLEngine(instance); got != c.mysql {
				t.Fatalf("IsMySQLEngine() = %v, want %v", got, c.mysql)
			}
			if got := IsPGEngine(instance); got != c.postgresql {
				t.Fatalf("IsPGEngine() = %v, want %v", got, c.postgresql)
			}
			if got := GetPortByEngine(c.engine); got != c.defaultPort {
				t.Fatalf("GetPortByEngine(%q) = %d, want %d", c.engine, got, c.defaultPort)
			}
			if c.clickhouse {
				if ClickHouseHTTPPort() != c.clickHTTP || ClickHouseNativePort() != c.clickNative ||
					ClickHouseSecureHTTPPort() != c.clickHTTPS || ClickHouseSecureNativePort() != c.clickNativeS {
					t.Fatalf("unexpected clickhouse ports")
				}
			}
		})
	}
}

func TestClickHouseNames(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{name: "statefulset", got: ClickHouseStatefulSetName("analytics", "ingest", 0, 1), want: "analytics-ch-ingest-s0-r1"},
		{name: "keeper statefulset", got: ClickHouseKeeperStatefulSetName("analytics", 2), want: "analytics-ch-keeper-2"},
		{name: "group headless", got: ClickHouseGroupHeadlessServiceName("analytics", "serving"), want: "analytics-ch-serving-headless"},
		{name: "group client", got: ClickHouseGroupClientServiceName("analytics", "serving"), want: "analytics-ch-serving"},
		{name: "keeper headless", got: ClickHouseKeeperHeadlessServiceName("analytics"), want: "analytics-ch-keeper-headless"},
		{name: "gateway deployment", got: ClickHouseGatewayDeploymentName("analytics"), want: "analytics-ch-gateway"},
		{name: "gateway service", got: ClickHouseGatewayServiceName("analytics"), want: "analytics-ch"},
		{name: "configmap", got: ClickHouseConfigMapName("analytics"), want: "analytics-ch-config"},
		{name: "secret", got: ClickHouseSecretName("analytics"), want: "analytics-ch-secret"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Fatalf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

func TestClickHouseLongNameIsDeterministicDNSLabel(t *testing.T) {
	instanceName := "analytics-with-a-very-long-name-that-would-exceed-kubernetes-dns-label-limits"
	first := ClickHouseStatefulSetName(instanceName, "serving-users", 12, 34)
	second := ClickHouseStatefulSetName(instanceName, "serving-users", 12, 34)
	if first != second {
		t.Fatalf("name is not deterministic: %q != %q", first, second)
	}
	if len(first) > 63 {
		t.Fatalf("name length = %d, want <= 63: %s", len(first), first)
	}
	if strings.HasPrefix(first, "-") || strings.HasSuffix(first, "-") {
		t.Fatalf("name must not start or end with dash: %s", first)
	}
}

func TestClickHouseHostLabels(t *testing.T) {
	labels := ClickHouseHostLabels("analytics", "ingest", 1, 2)
	want := map[string]string{
		LabelInstance:                 "analytics",
		LabelClickHouseEngine:         ClickHouseEngine,
		LabelClickHouseComponent:      ClickHouseComponentClickHouse,
		LabelClickHouseComputeGroup:   "ingest",
		LabelClickHouseDataShard:      "1",
		LabelClickHouseReplica:        "2",
	}
	for key, value := range want {
		if labels[key] != value {
			t.Fatalf("labels[%q] = %q, want %q", key, labels[key], value)
		}
	}
}
