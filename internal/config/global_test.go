package config

import (
	"testing"

	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/util"
)

func TestGetParameterReportConfigDefaults(t *testing.T) {
	got := (&GlobalConfig{}).GetParameterReportConfig()
	if !got.Enabled {
		t.Fatalf("parameter reporter should be enabled by default")
	}
	if got.HostFile != naming.ParameterReportHostPath {
		t.Fatalf("unexpected host file: %s", got.HostFile)
	}
	if got.TokenFile != naming.ParameterReportTokenPath {
		t.Fatalf("unexpected token file: %s", got.TokenFile)
	}
	if got.CatalogFile != naming.ParameterReportCatalogPath {
		t.Fatalf("unexpected catalog file: %s", got.CatalogFile)
	}
	if got.IntervalSeconds != 60 || got.TimeoutSeconds != 10 {
		t.Fatalf("unexpected defaults: %#v", got)
	}
}

func TestGetParameterReportConfigOverrides(t *testing.T) {
	cfg := &GlobalConfig{ParameterReport: ParameterReportConfig{
		Enabled:         util.Bool(false),
		HostFile:        "/custom/report_host",
		TokenFile:       "/custom/report_token",
		CatalogFile:     "/custom/catalog.json",
		IntervalSeconds: 30,
		TimeoutSeconds:  5,
	}}
	got := cfg.GetParameterReportConfig()
	if got.Enabled {
		t.Fatalf("expected disabled reporter")
	}
	if got.HostFile != "/custom/report_host" ||
		got.TokenFile != "/custom/report_token" ||
		got.CatalogFile != "/custom/catalog.json" {
		t.Fatalf("unexpected custom paths: %#v", got)
	}
	if got.IntervalSeconds != 30 || got.TimeoutSeconds != 5 {
		t.Fatalf("unexpected custom timings: %#v", got)
	}
}
