package naming

import "testing"

func TestPostgreSQLPortAndEngineAliases(t *testing.T) {
	for _, engine := range []string{PostgresEngine, PostgresEnginePG, "PostgreSQL", "PG"} {
		if got := GetPortByEngine(engine); got != 5432 {
			t.Fatalf("GetPortByEngine(%q) = %d, want 5432", engine, got)
		}
	}
}
