package config

import (
	"strings"
	"testing"
)

func TestMySQLFileLogsUseCanonicalDataRoot(t *testing.T) {
	for name, config := range map[string]string{
		"mysql57": MySQL57ConfTmpl,
		"mysql8":  MySQL8ConfTmpl,
	} {
		if strings.Contains(config, "/var/log/mysql/") {
			t.Fatalf("%s still configures legacy log directory", name)
		}
		for _, path := range []string{"/kdbdata/log/", "slow.log"} {
			if !strings.Contains(config, path) {
				t.Fatalf("%s does not contain %q", name, path)
			}
		}
	}
}
