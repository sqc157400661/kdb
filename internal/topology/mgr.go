package topology

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
)

const (
	DefaultMGRGroupPort        int32 = 33061
	DefaultMGRBootstrapOrdinal int32 = 0
)

type MGRMode string

const (
	MGRModeSinglePrimary MGRMode = v1.MySQLMGRModeSinglePrimary
	MGRModeMultiPrimary  MGRMode = v1.MySQLMGRModeMultiPrimary
)

type MGRConfig struct {
	Enabled          bool
	Mode             MGRMode
	GroupName        string
	BootstrapOrdinal int32
	GroupPort        int32
	Seeds            string
}

func ResolveMGRConfig(instance *v1.KDBInstance) (MGRConfig, error) {
	cfg := MGRConfig{
		Enabled:          instance != nil && naming.DeployArch(instance) == naming.MySQLMGRDeployArch,
		Mode:             MGRModeSinglePrimary,
		BootstrapOrdinal: DefaultMGRBootstrapOrdinal,
		GroupPort:        DefaultMGRGroupPort,
	}
	if !cfg.Enabled {
		return cfg, nil
	}

	mergeMGRLegacyConfig(&cfg, instance.Spec.Config)
	if instance.Spec.MySQL != nil && instance.Spec.MySQL.MGR != nil {
		mergeMGRTypedSpec(&cfg, instance.Spec.MySQL.MGR)
	}
	if cfg.GroupName == "" {
		cfg.GroupName = StableMGRGroupName(instance.Namespace, instance.Name)
	}
	cfg.Seeds = BuildMGRSeeds(instance, cfg.GroupPort)
	return cfg, cfg.Validate(replicasOf(instance))
}

func (c MGRConfig) Validate(replicas int) error {
	if !c.Enabled {
		return nil
	}
	switch c.Mode {
	case MGRModeSinglePrimary, MGRModeMultiPrimary:
	default:
		return fmt.Errorf("invalid mysql mgr mode: %s", c.Mode)
	}
	if c.GroupName == "" {
		return fmt.Errorf("mysql mgr groupName is empty")
	}
	if _, err := uuid.Parse(c.GroupName); err != nil {
		return fmt.Errorf("invalid mysql mgr groupName: %w", err)
	}
	if c.BootstrapOrdinal < 0 {
		return fmt.Errorf("mysql mgr bootstrapOrdinal must be greater than or equal to 0")
	}
	if replicas > 0 && int(c.BootstrapOrdinal) >= replicas {
		return fmt.Errorf("mysql mgr bootstrapOrdinal must be less than replicas")
	}
	if c.GroupPort < 1024 || c.GroupPort > 65535 {
		return fmt.Errorf("mysql mgr groupPort must be between 1024 and 65535")
	}
	return nil
}

func BuildMGRSeeds(instance *v1.KDBInstance, groupPort int32) string {
	if instance == nil || instance.Spec.InstanceSet.Replicas == nil {
		return ""
	}
	replicas := int(*instance.Spec.InstanceSet.Replicas)
	if replicas <= 0 {
		return ""
	}
	members := make([]string, 0, replicas)
	for i := 0; i < replicas; i++ {
		members = append(members, BuildMGRLocalAddress(instance, i, groupPort))
	}
	return strings.Join(members, ",")
}

func BuildMGRLocalAddress(instance *v1.KDBInstance, ordinal int, groupPort int32) string {
	if instance == nil {
		return ""
	}
	host := naming.InstancePodHost(instance.Name, naming.InstancePodServiceName(instance.Name), instance.Namespace, ordinal)
	return fmt.Sprintf("%s:%d", host, groupPort)
}

func StableMGRGroupName(namespace, name string) string {
	key := strings.TrimSpace(namespace) + "/" + strings.TrimSpace(name)
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(key)).String()
}

func mergeMGRTypedSpec(cfg *MGRConfig, spec *v1.MySQLMGRSpec) {
	if mode := strings.TrimSpace(spec.Mode); mode != "" {
		cfg.Mode = MGRMode(mode)
	}
	if groupName := strings.TrimSpace(spec.GroupName); groupName != "" {
		cfg.GroupName = groupName
	}
	if spec.BootstrapOrdinal != nil {
		cfg.BootstrapOrdinal = *spec.BootstrapOrdinal
	}
	if spec.GroupPort != nil {
		cfg.GroupPort = *spec.GroupPort
	}
}

func mergeMGRLegacyConfig(cfg *MGRConfig, legacy map[string]string) {
	if legacy == nil {
		return
	}
	if mode := strings.TrimSpace(legacy["mysql.mgr.mode"]); mode != "" {
		cfg.Mode = MGRMode(mode)
	}
	if groupName := strings.TrimSpace(legacy["mysql.mgr.groupName"]); groupName != "" {
		cfg.GroupName = groupName
	}
	if raw := strings.TrimSpace(legacy["mysql.mgr.bootstrapOrdinal"]); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 32); err == nil {
			cfg.BootstrapOrdinal = int32(v)
		}
	}
	if raw := strings.TrimSpace(legacy["mysql.mgr.groupPort"]); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 32); err == nil {
			cfg.GroupPort = int32(v)
		}
	}
}

func replicasOf(instance *v1.KDBInstance) int {
	if instance == nil || instance.Spec.InstanceSet.Replicas == nil {
		return 0
	}
	return int(*instance.Spec.InstanceSet.Replicas)
}
