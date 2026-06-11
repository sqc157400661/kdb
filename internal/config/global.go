package config

import (
	"fmt"
	"strings"

	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/util"
)

type DBConfig struct {
	RootUser     string `json:"root_user" yaml:"root_user"`
	RootPassword string `json:"root_password" yaml:"root_password"`
	ReplUser     string `yaml:"repl_user" json:"repl_user"`
	ReplPassword string `yaml:"repl_password" json:"repl_password"`
}

const (
	HostResolveModeDNS = "dns"
	HostResolveModeIP  = "ip"
)

type GlobalConfig struct {
	DB                  DBConfig              `json:"db" yaml:"db"`
	MySQLInstanceConfig InstanceConfig        `json:"mysql_instance_config" yaml:"mysql_instance_config"`
	HostResolveMode     string                `json:"host_resolve_mode" yaml:"host_resolve_mode"`
	ParameterReport     ParameterReportConfig `json:"parameter_report" yaml:"parameter_report"`
}

type ParameterReportConfig struct {
	Enabled         *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	HostFile        string `json:"host_file,omitempty" yaml:"host_file,omitempty"`
	TokenFile       string `json:"token_file,omitempty" yaml:"token_file,omitempty"`
	CatalogFile     string `json:"catalog_file,omitempty" yaml:"catalog_file,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty" yaml:"interval_seconds,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
}

type ResolvedParameterReportConfig struct {
	Enabled         bool
	HostFile        string
	TokenFile       string
	CatalogFile     string
	IntervalSeconds int
	TimeoutSeconds  int
}

type InstanceImage struct {
	Main    string `json:"main" yaml:"main"`
	Sidecar string `json:"sidecar" yaml:"sidecar"`
	Monitor string `json:"monitor" yaml:"monitor"`
	Backup  string `json:"backup" yaml:"backup"`
}

type FullVersion string

type InstanceConfig struct {
	VersionImagesMap map[FullVersion]InstanceImage     `json:"version_images_map" yaml:"version_images_map"`
	GlobalConfig     map[string]string                 `json:"global_config" yaml:"global_config"`
	VersionConfig    map[FullVersion]map[string]string `json:"version_config" yaml:"version_config"`
}

func (c *GlobalConfig) GetHostResolveMode() string {
	if c == nil {
		return HostResolveModeDNS
	}
	switch strings.ToLower(c.HostResolveMode) {
	case HostResolveModeIP:
		return HostResolveModeIP
	case HostResolveModeDNS:
		fallthrough
	default:
		return HostResolveModeDNS
	}
}

func (c *GlobalConfig) GetParameterReportConfig() ResolvedParameterReportConfig {
	out := ResolvedParameterReportConfig{
		Enabled:         true,
		HostFile:        naming.ParameterReportHostPath,
		TokenFile:       naming.ParameterReportTokenPath,
		CatalogFile:     naming.ParameterReportCatalogPath,
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
	}
	if c == nil {
		return out
	}
	cfg := c.ParameterReport
	if cfg.Enabled != nil {
		out.Enabled = *cfg.Enabled
	}
	if strings.TrimSpace(cfg.HostFile) != "" {
		out.HostFile = strings.TrimSpace(cfg.HostFile)
	}
	if strings.TrimSpace(cfg.TokenFile) != "" {
		out.TokenFile = strings.TrimSpace(cfg.TokenFile)
	}
	if strings.TrimSpace(cfg.CatalogFile) != "" {
		out.CatalogFile = strings.TrimSpace(cfg.CatalogFile)
	}
	if cfg.IntervalSeconds > 0 {
		out.IntervalSeconds = cfg.IntervalSeconds
	}
	if cfg.TimeoutSeconds > 0 {
		out.TimeoutSeconds = cfg.TimeoutSeconds
	}
	return out
}

func (c *GlobalConfig) GetDBConfig(engine string, fullVersion string) map[string]string {
	if c == nil {
		return nil
	}
	global := c.MySQLInstanceConfig.GlobalConfig
	switch engine {
	case naming.MySQLEngine:
		versionConfig := c.MySQLInstanceConfig.VersionConfig
		if len(versionConfig) == 0 {
			return global
		}
		if conf, ok := versionConfig[FullVersion(fullVersion)]; ok {
			return util.UnsafeMergeMap(conf, global)
		}
	case naming.PostgresEngine:
	default:
		return global
	}
	return global
}

func (c *GlobalConfig) GetMainImage(engine string, fullVersion string) (images string, err error) {
	image, err := c.getImage(engine, fullVersion)
	if err != nil {
		return "", err
	}
	return image.Main, nil
}

func (c *GlobalConfig) GetSidecarImage(engine string, fullVersion string) (images string, err error) {
	image, err := c.getImage(engine, fullVersion)
	if err != nil {
		return "", err
	}
	return image.Sidecar, nil
}

func (c *GlobalConfig) GetMonitorImage(engine string, fullVersion string) (images string, err error) {
	image, err := c.getImage(engine, fullVersion)
	if err != nil {
		return "", err
	}
	return image.Monitor, nil
}

func (c *GlobalConfig) GetBackupImage(engine string, fullVersion string) (images string, err error) {
	image, err := c.getImage(engine, fullVersion)
	if err != nil {
		return "", err
	}
	return image.Backup, nil
}

func (c *GlobalConfig) getImage(engine string, fullVersion string) (*InstanceImage, error) {
	if c == nil {
		return nil, fmt.Errorf("nil config")
	}
	switch engine {
	case naming.MySQLEngine:
		imagesMap := c.MySQLInstanceConfig.VersionImagesMap
		if len(imagesMap) == 0 {
			return nil, fmt.Errorf("no version_images map")
		}
		if image, ok := imagesMap[FullVersion(fullVersion)]; ok {
			return &image, nil
		}
	case naming.PostgresEngine:
	default:
		return nil, fmt.Errorf("unknown engine %q", engine)
	}
	return nil, fmt.Errorf("not found image config")
}
