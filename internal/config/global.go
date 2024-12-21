package config

import (
	"fmt"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/util"
)

type DBConfig struct {
	RootUser     string `json:"root_user" yaml:"root_user"`
	RootPassword string `json:"root_password" yaml:"root_password"`
	ReplUser     string `yaml:"repl_user" json:"repl_user"`
	ReplPassword string `yaml:"repl_password" json:"repl_password"`
}
type GlobalConfig struct {
	DB                  DBConfig       `json:"db" yaml:"db"`
	MySQLInstanceConfig InstanceConfig `json:"mysql_instance_config" yaml:"mysql_instance_config"`
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
