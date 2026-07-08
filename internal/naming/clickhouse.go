package naming

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	ClickHouseComponentClickHouse = "clickhouse"
	ClickHouseComponentKeeper     = "keeper"
	ClickHouseComponentGateway    = "gateway"
	ClickHouseComponentSidecar    = "sidecar"
	ClickHouseComponentBackup     = "backup"
)

const (
	LabelClickHouseEngine       = "kdb.com/engine"
	LabelClickHouseComponent    = "kdb.com/component"
	LabelClickHouseComputeGroup = "kdb.com/compute-group"
	LabelClickHouseDataShard    = "kdb.com/data-shard"
	LabelClickHouseReplica      = "kdb.com/replica"
	LabelClickHouseRoutable     = "kdb.com/routable"
)

func ClickHouseHTTPPort() int32 {
	return 8123
}

func ClickHouseNativePort() int32 {
	return 9000
}

func ClickHouseKeeperClientPort() int32 {
	return 9181
}

func ClickHouseKeeperRaftPort() int32 {
	return 9234
}

func ClickHouseGatewayHTTPPort() int32 {
	return 8123
}

func ClickHouseGatewayMetricsPort() int32 {
	return 9090
}

func ClickHouseSecureHTTPPort() int32 {
	return 8443
}

func ClickHouseSecureNativePort() int32 {
	return 9440
}

func ClickHouseStatefulSetName(instanceName, group string, shard, replica int32) string {
	return dnsLabel(fmt.Sprintf("%s-ch-%s-s%d-r%d", instanceName, group, shard, replica))
}

func ClickHouseKeeperStatefulSetName(instanceName string, member int32) string {
	return dnsLabel(fmt.Sprintf("%s-ch-keeper-%d", instanceName, member))
}

func ClickHouseKeeperConfigMapName(instanceName string, member int32) string {
	return dnsLabel(fmt.Sprintf("%s-ch-keeper-%d-config", instanceName, member))
}

func ClickHouseGroupHeadlessServiceName(instanceName, group string) string {
	return dnsLabel(fmt.Sprintf("%s-ch-%s-headless", instanceName, group))
}

func ClickHouseGroupClientServiceName(instanceName, group string) string {
	return dnsLabel(fmt.Sprintf("%s-ch-%s", instanceName, group))
}

func ClickHouseKeeperHeadlessServiceName(instanceName string) string {
	return dnsLabel(fmt.Sprintf("%s-ch-keeper-headless", instanceName))
}

func ClickHouseGatewayDeploymentName(instanceName string) string {
	return dnsLabel(fmt.Sprintf("%s-ch-gateway", instanceName))
}

func ClickHouseGatewayConfigMapName(instanceName string) string {
	return dnsLabel(fmt.Sprintf("%s-ch-gateway-config", instanceName))
}

func ClickHouseGatewayServiceName(instanceName string) string {
	return dnsLabel(fmt.Sprintf("%s-ch", instanceName))
}

func ClickHouseConfigMapName(instanceName string) string {
	return dnsLabel(fmt.Sprintf("%s-ch-config", instanceName))
}

func ClickHouseGroupConfigMapName(instanceName, group string) string {
	return dnsLabel(fmt.Sprintf("%s-ch-%s-config", instanceName, group))
}

func ClickHouseSecretName(instanceName string) string {
	return dnsLabel(fmt.Sprintf("%s-ch-secret", instanceName))
}

func ClickHouseLabels(instanceName, component string) map[string]string {
	return map[string]string{
		LabelInstance:            instanceName,
		LabelClickHouseEngine:    ClickHouseEngine,
		LabelClickHouseComponent: component,
	}
}

func ClickHouseHostLabels(instanceName, group string, shard, replica int32) map[string]string {
	labels := ClickHouseLabels(instanceName, ClickHouseComponentClickHouse)
	labels[LabelClickHouseComputeGroup] = group
	labels[LabelClickHouseDataShard] = fmt.Sprintf("%d", shard)
	labels[LabelClickHouseReplica] = fmt.Sprintf("%d", replica)
	return labels
}

func ClickHouseKeeperLabels(instanceName string, member int32) map[string]string {
	labels := ClickHouseLabels(instanceName, ClickHouseComponentKeeper)
	labels[LabelClickHouseReplica] = fmt.Sprintf("%d", member)
	return labels
}

func dnsLabel(name string) string {
	normalized := normalizeDNSLabel(name)
	if len(normalized) <= 63 {
		return normalized
	}
	sum := sha1.Sum([]byte(normalized))
	hash := hex.EncodeToString(sum[:])[:10]
	prefix := strings.TrimRight(normalized[:52], "-")
	if prefix == "" {
		prefix = "x"
	}
	return prefix + "-" + hash
}

func normalizeDNSLabel(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	normalized := strings.Trim(b.String(), "-")
	if normalized == "" {
		return "x"
	}
	return normalized
}
