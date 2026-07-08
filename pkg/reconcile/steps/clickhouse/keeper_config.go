package clickhouse

import (
	"fmt"
	"strings"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const clickHouseKeeperConfigKey = "keeper.xml"

type keeperMember struct {
	Index int32
	ID    int32
	Host  string
}

func buildKeeperConfigMaps(instance *v1.KDBInstance) ([]*corev1.ConfigMap, error) {
	members, err := desiredKeeperMembers(instance)
	if err != nil {
		return nil, err
	}
	configMaps := make([]*corev1.ConfigMap, 0, len(members))
	for _, member := range members {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Namespace: instance.Namespace,
			Name:      naming.ClickHouseKeeperConfigMapName(instance.Name, member.Index),
			Labels:    naming.Merge(instance.Labels, naming.ClickHouseKeeperLabels(instance.Name, member.Index)),
		}}
		cm.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
		cm.Data = map[string]string{
			clickHouseKeeperConfigKey: naming.XMLGeneratedWarning + renderKeeperConfig(members, member),
		}
		configMaps = append(configMaps, cm)
	}
	return configMaps, nil
}

func desiredKeeperMembers(instance *v1.KDBInstance) ([]keeperMember, error) {
	replicas, err := desiredKeeperReplicas(instance)
	if err != nil {
		return nil, err
	}
	members := make([]keeperMember, 0, replicas)
	for i := int32(0); i < replicas; i++ {
		members = append(members, keeperMember{
			Index: i,
			ID:    i + 1,
			Host:  keeperMemberDNS(instance, i),
		})
	}
	return members, nil
}

func desiredKeeperReplicas(instance *v1.KDBInstance) (int32, error) {
	if instance == nil || instance.Spec.ClickHouse == nil {
		return 0, fmt.Errorf("spec.clickhouse is required when engine is clickhouse")
	}
	keeper := instance.Spec.ClickHouse.Keeper
	if keeper.Mode != v1.ClickHouseKeeperDedicated {
		return 0, nil
	}
	replicas := int32(3)
	if keeper.Replicas != nil {
		replicas = *keeper.Replicas
	}
	switch replicas {
	case 1, 3, 5:
		return replicas, nil
	default:
		return 0, fmt.Errorf("clickhouse dedicated keeper replicas must be 1, 3, or 5")
	}
}

func keeperMemberDNS(instance *v1.KDBInstance, member int32) string {
	return fmt.Sprintf("%s-0.%s.%s.svc", naming.ClickHouseKeeperStatefulSetName(instance.Name, member), naming.ClickHouseKeeperHeadlessServiceName(instance.Name), instance.Namespace)
}

func renderKeeperConfig(members []keeperMember, local keeperMember) string {
	var b strings.Builder
	b.WriteString("<clickhouse>\n")
	b.WriteString("  <listen_host>0.0.0.0</listen_host>\n")
	b.WriteString("  <keeper_server>\n")
	b.WriteString(fmt.Sprintf("    <tcp_port>%d</tcp_port>\n", naming.ClickHouseKeeperClientPort()))
	b.WriteString(fmt.Sprintf("    <server_id>%d</server_id>\n", local.ID))
	b.WriteString("    <log_storage_path>/var/lib/clickhouse-keeper/coordination/log</log_storage_path>\n")
	b.WriteString("    <snapshot_storage_path>/var/lib/clickhouse-keeper/coordination/snapshots</snapshot_storage_path>\n")
	b.WriteString("    <coordination_settings>\n")
	b.WriteString("      <operation_timeout_ms>10000</operation_timeout_ms>\n")
	b.WriteString("      <session_timeout_ms>30000</session_timeout_ms>\n")
	b.WriteString("      <raft_logs_level>warning</raft_logs_level>\n")
	b.WriteString("    </coordination_settings>\n")
	b.WriteString("    <raft_configuration>\n")
	for _, member := range members {
		b.WriteString("      <server>\n")
		b.WriteString(fmt.Sprintf("        <id>%d</id>\n", member.ID))
		b.WriteString(fmt.Sprintf("        <hostname>%s</hostname>\n", member.Host))
		b.WriteString(fmt.Sprintf("        <port>%d</port>\n", naming.ClickHouseKeeperRaftPort()))
		b.WriteString("      </server>\n")
	}
	b.WriteString("    </raft_configuration>\n")
	b.WriteString("  </keeper_server>\n")
	b.WriteString("</clickhouse>\n")
	return b.String()
}
