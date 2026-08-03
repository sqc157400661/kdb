package clickhouse

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	clickHouseRemoteServersKey = "remote_servers.xml"
	clickHouseMacrosKey        = "macros.xml"
	clickHouseKeeperKey        = "keeper.xml"
	clickHouseNetworkKey       = "network.xml"
	clickHouseInterserverKey   = "interserver.xml"
	clickHouseStoragePolicyKey = "storage_policy.xml"
	clickHouseLoggingKey       = "logging.xml"
	clickHouseUsersKey         = "users.xml"
	clickHouseProfilesKey      = "profiles.xml"
	clickHouseQuotasKey        = "quotas.xml"
	clickHouseSidecarKey       = "sidecar.yaml"
)

func buildStandaloneConfigMap(instance *v1.KDBInstance) (*corev1.ConfigMap, error) {
	configMaps, err := buildClickHouseConfigMaps(instance)
	if err != nil {
		return nil, err
	}
	if len(configMaps) == 0 {
		return nil, fmt.Errorf("clickhouse computeGroups must not be empty")
	}
	return configMaps[0], nil
}

func buildClickHouseConfigMaps(instance *v1.KDBInstance) ([]*corev1.ConfigMap, error) {
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		return nil, err
	}
	keeperConfig, err := renderClickHouseKeeper(instance)
	if err != nil {
		return nil, err
	}
	configMaps := make([]*corev1.ConfigMap, 0, len(instance.Spec.ClickHouse.ComputeGroups))
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		labels := naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentClickHouse)
		labels[naming.LabelClickHouseComputeGroup] = group.Name
		configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Namespace: instance.Namespace, Name: naming.ClickHouseGroupConfigMapName(instance.Name, group.Name),
			Labels: naming.Merge(instance.Labels, labels), Annotations: naming.Merge(instance.Annotations),
		}}
		if configMap.Annotations == nil {
			configMap.Annotations = map[string]string{}
		}
		configMap.Annotations[annotationReloadableRevision] = reloadableConfigRevision(instance)
		configMap.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
		configMap.Data = map[string]string{
			clickHouseRemoteServersKey: naming.XMLGeneratedWarning + renderClickHouseRemoteServers(instance, plans, group.Name),
			clickHouseMacrosKey:        naming.XMLGeneratedWarning + renderClickHouseMacros(),
			clickHouseKeeperKey:        naming.XMLGeneratedWarning + keeperConfig,
			clickHouseNetworkKey:       naming.XMLGeneratedWarning + renderClickHouseNetwork(),
			clickHouseInterserverKey:   naming.XMLGeneratedWarning + "<clickhouse><interserver_http_host from_env=\"POD_IP\"/></clickhouse>\n",
			clickHouseStoragePolicyKey: naming.XMLGeneratedWarning + "<clickhouse><storage_configuration><policies><default><volumes><default><disk>default</disk></default></volumes></default></policies></storage_configuration></clickhouse>\n",
			clickHouseLoggingKey:       naming.XMLGeneratedWarning + "<clickhouse><logger><log>" + naming.DatabaseLogRoot + "/clickhouse-server.log</log><errorlog>" + naming.DatabaseLogRoot + "/clickhouse-server-error.log</errorlog></logger></clickhouse>\n",
			clickHouseUsersKey:         naming.XMLGeneratedWarning + renderStandaloneUsers(),
			clickHouseProfilesKey:      naming.XMLGeneratedWarning + renderClickHouseProfiles(),
			clickHouseQuotasKey:        naming.XMLGeneratedWarning + "<clickhouse><quotas><default/></quotas></clickhouse>\n",
			clickHouseSidecarKey:       naming.YamlGeneratedWarning + renderStandaloneSidecarConfig(instance),
		}
		configMaps = append(configMaps, configMap)
	}
	return configMaps, nil
}

func buildStandaloneSecret(instance *v1.KDBInstance) (*corev1.Secret, error) {
	if _, err := buildClickHouseHostPlans(instance); err != nil {
		return nil, err
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace:   instance.Namespace,
		Name:        naming.ClickHouseSecretName(instance.Name),
		Labels:      naming.Merge(instance.Labels, naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentClickHouse)),
		Annotations: instance.Annotations,
	}}
	secret.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
	secret.Type = corev1.SecretTypeOpaque
	data, err := newClickHouseCredentialData()
	if err != nil {
		return nil, err
	}
	secret.Data = data
	return secret, nil
}

func newClickHouseCredentialData() (map[string][]byte, error) {
	data := map[string][]byte{
		"admin-username":   []byte("kdb_admin"),
		"schema-username":  []byte("kdb_schema"),
		"serving-username": []byte("kdb_serving"),
		"adhoc-username":   []byte("kdb_adhoc"),
	}
	for _, key := range []string{"admin-password", "schema-password", "serving-password", "adhoc-password"} {
		raw := make([]byte, 24)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		data[key] = []byte(base64.RawURLEncoding.EncodeToString(raw))
	}
	return data, nil
}

func renderClickHouseKeeper(instance *v1.KDBInstance) (string, error) {
	if instance == nil || instance.Spec.ClickHouse == nil {
		return "", fmt.Errorf("spec.clickhouse is required when engine is clickhouse")
	}
	hosts := []string{}
	switch instance.Spec.ClickHouse.Keeper.Mode {
	case v1.ClickHouseKeeperDedicated:
		members, err := desiredKeeperMembers(instance)
		if err != nil {
			return "", err
		}
		for _, member := range members {
			hosts = append(hosts, member.Host)
		}
	case v1.ClickHouseKeeperSharedRef:
		if instance.Spec.ClickHouse.Keeper.Ref == nil || strings.TrimSpace(instance.Spec.ClickHouse.Keeper.Ref.Name) == "" {
			return "", fmt.Errorf("clickhouse shared Keeper requires ref.name")
		}
		hosts = append(hosts, strings.TrimSpace(instance.Spec.ClickHouse.Keeper.Ref.Name))
	default:
		return "", fmt.Errorf("unsupported clickhouse keeper mode: %s", instance.Spec.ClickHouse.Keeper.Mode)
	}
	var body strings.Builder
	body.WriteString("<clickhouse>\n  <keeper_server remove=\"remove\"/>\n  <zookeeper>\n")
	for index, host := range hosts {
		body.WriteString(fmt.Sprintf("    <node index=\"%d\"><host>%s</host><port>%d</port></node>\n", index+1, host, naming.ClickHouseKeeperClientPort()))
	}
	body.WriteString("  </zookeeper>\n</clickhouse>\n")
	return body.String(), nil
}

func renderStandaloneRemoteServers(instance *v1.KDBInstance, group v1.ClickHouseComputeGroupSpec) string {
	return renderClickHouseRemoteServers(instance, []clickHouseHostPlan{{
		Group:       group,
		Shard:       0,
		Replica:     0,
		StatefulSet: naming.ClickHouseStatefulSetName(instance.Name, group.Name, 0, 0),
	}}, group.Name)
}

func renderClickHouseRemoteServers(instance *v1.KDBInstance, plans []clickHouseHostPlan, localGroup string) string {
	groups := map[string][]clickHouseHostPlan{}
	for _, plan := range plans {
		groups[plan.Group.Name] = append(groups[plan.Group.Name], plan)
	}
	var body string
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		groupPlans := groups[group.Name]
		if len(groupPlans) == 0 {
			continue
		}
		body += fmt.Sprintf("    <kdb_%s_group>\n", group.Name)
		for shard := int32(0); shard < instance.Spec.ClickHouse.DataShards; shard++ {
			body += "      <shard>\n        <internal_replication>true</internal_replication>\n"
			for _, plan := range groupPlans {
				if plan.Shard != shard {
					continue
				}
				body += renderClickHouseRemoteReplica(instance, plan)
			}
			body += "      </shard>\n"
		}
		body += fmt.Sprintf("    </kdb_%s_group>\n", group.Name)
	}
	if localPlans := groups[localGroup]; len(localPlans) > 0 {
		body += renderClickHouseCluster("kdb_local_group", instance.Spec.ClickHouse.DataShards, localPlans, instance)
	}
	body += "    <kdb_all_replicas>\n"
	for shard := int32(0); shard < instance.Spec.ClickHouse.DataShards; shard++ {
		body += "      <shard>\n        <internal_replication>true</internal_replication>\n"
		for _, plan := range plans {
			if plan.Shard != shard {
				continue
			}
			body += renderClickHouseRemoteReplica(instance, plan)
		}
		body += "      </shard>\n"
	}
	body += "    </kdb_all_replicas>\n"
	return "<clickhouse>\n  <remote_servers>\n" + body + "  </remote_servers>\n</clickhouse>\n"
}

func renderClickHouseCluster(name string, dataShards int32, plans []clickHouseHostPlan, instance *v1.KDBInstance) string {
	body := fmt.Sprintf("    <%s>\n", name)
	for shard := int32(0); shard < dataShards; shard++ {
		body += "      <shard>\n        <internal_replication>true</internal_replication>\n"
		for _, plan := range plans {
			if plan.Shard == shard {
				body += renderClickHouseRemoteReplica(instance, plan)
			}
		}
		body += "      </shard>\n"
	}
	return body + fmt.Sprintf("    </%s>\n", name)
}

func renderClickHouseRemoteReplica(instance *v1.KDBInstance, plan clickHouseHostPlan) string {
	return fmt.Sprintf("        <replica><host>%s</host><port>%d</port><user>kdb_admin</user><password from_env=\"CLICKHOUSE_ADMIN_PASSWORD\"/></replica>\n", clickHouseHostDNS(instance, plan), naming.ClickHouseNativePort())
}

func clickHouseHostDNS(instance *v1.KDBInstance, plan clickHouseHostPlan) string {
	return fmt.Sprintf("%s-0.%s.%s.svc", plan.StatefulSet, naming.ClickHouseGroupHeadlessServiceName(instance.Name, plan.Group.Name), instance.Namespace)
}

func renderStandaloneMacros(instance *v1.KDBInstance, group v1.ClickHouseComputeGroupSpec) string {
	return renderClickHouseMacros()
}

func renderClickHouseNetwork() string {
	return `<clickhouse>
  <listen_host>0.0.0.0</listen_host>
</clickhouse>
`
}

func renderClickHouseMacros() string {
	return `<clickhouse>
  <macros>
    <shard from_env="KDB_CLICKHOUSE_SHARD"/>
    <replica from_env="KDB_CLICKHOUSE_REPLICA"/>
    <cluster from_env="KDB_CLICKHOUSE_GROUP"/>
    <instance from_env="KDB_INSTANCE_NAME"/>
    <replicated_database_path>/kdb/{instance}/databases/{shard}</replicated_database_path>
    <replicated_table_path>/kdb/{instance}/tables/{shard}/{database}/{table}</replicated_table_path>
  </macros>
</clickhouse>
`
}

func renderStandaloneUsers() string {
	return `<clickhouse>
  <users>
	    <default>
	      <password from_env="CLICKHOUSE_ADMIN_PASSWORD"/>
      <profile>default</profile>
      <quota>default</quota>
      <access_management>1</access_management>
    </default>
	    <kdb_admin>
	      <password from_env="CLICKHOUSE_ADMIN_PASSWORD"/>
      <profile>default</profile>
      <quota>default</quota>
      <grants>
        <query>GRANT ALL ON *.* WITH GRANT OPTION</query>
      </grants>
    </kdb_admin>
	    <kdb_schema>
	      <password from_env="CLICKHOUSE_SCHEMA_PASSWORD"/>
      <profile>default</profile>
      <quota>default</quota>
      <access_management>1</access_management>
    </kdb_schema>
	    <kdb_serving>
	      <password from_env="CLICKHOUSE_SERVING_PASSWORD"/>
      <profile>readonly</profile>
      <quota>default</quota>
      <access_management>0</access_management>
    </kdb_serving>
	    <kdb_adhoc>
	      <password from_env="CLICKHOUSE_ADHOC_PASSWORD"/>
      <profile>readonly</profile>
      <quota>default</quota>
      <access_management>0</access_management>
    </kdb_adhoc>
  </users>
</clickhouse>
`
}

func renderClickHouseProfiles() string {
	return `<clickhouse>
  <profiles>
    <default/>
    <readonly>
      <readonly>1</readonly>
    </readonly>
  </profiles>
</clickhouse>
`
}

func renderStandaloneSidecarConfig(instance *v1.KDBInstance) string {
	backupRunnerURL := ""
	if clickHouseBackupRunnerEnabled(instance) {
		backupRunnerURL = fmt.Sprintf("http://127.0.0.1:%d", clickHouseBackupRunnerPort)
	}
	return fmt.Sprintf(`clickhouse:
  http:
    address: "http://127.0.0.1:8123"
  config:
    root: "/etc/clickhouse-server"
    revision: %q
  backupRunner:
    url: %q
`, reloadableConfigRevision(instance), backupRunnerURL)
}
