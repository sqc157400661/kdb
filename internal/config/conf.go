package config

import (
	_ "embed"
	"github.com/sqc157400661/kdb/internal/naming"
	"strings"
)

var (

	// MySQL8ConfTmpl https://dev.mysql.com/doc/refman/8.0/en/server-configuration-defaults.html
	//go:embed tmpl/ini_mysql8.tmpl
	MySQL8ConfTmpl string

	// MySQL57ConfTmpl https://dev.mysql.com/doc/refman/5.7/en/server-configuration-defaults.html
	//go:embed tmpl/ini_mysql57.tmpl
	MySQL57ConfTmpl string

	//go:embed tmpl/instance.tmpl
	InstanceConfigTmpl string
)

func GetPortByEngine(engine string) int32 {
	if strings.ToLower(engine) == naming.MySQLEngine {
		return 3306
	}
	return 0
}
