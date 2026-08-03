package naming

import "k8s.io/apimachinery/pkg/api/resource"

var (
	OneMillicore = resource.MustParse("1m")
	OneMebibyte  = resource.MustParse("1Mi")
)

const (
	// ContainerDatabase is the name of the container running KDB database container
	ContainerDatabase = "database"

	ContainerSidecar = "mgr"

	// ContainerPostgreSQLHA is the single PostgreSQL Pod-local management runtime.
	ContainerPostgreSQLHA = "kdb-ha"

	ContainerMySQLExporter = "mysql-exporter"

	ContainerPostgreSQLExporter = "postgresql-exporter"
	//
	//ContainerInit = "init"
	//
	//ContainerMonitor = "monitor"
)

const (
	// PortDatabase is the name of a port that connects to kdb instance.
	PortDatabase = "database"

	PortSidecarMetrics = "mgr-metrics"

	PortMySQLMetrics = "mysql-metrics"

	// PortPostgreSQLMetrics is intentionally <= 15 characters: Kubernetes
	// ContainerPort names are DNS_LABELs with a 15-character limit. The old
	// descriptive value "postgresql-metrics" could never be persisted in a
	// StatefulSet and is retained only in migration documentation/search hints.
	PortPostgreSQLMetrics = "pg-metrics"

	PortPostgreSQLHA = "kdb-ha"
)
