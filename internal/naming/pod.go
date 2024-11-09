package naming

import "k8s.io/apimachinery/pkg/api/resource"

var (
	OneMillicore = resource.MustParse("1m")
	OneMebibyte  = resource.MustParse("1Mi")
)

const (
	// ContainerDatabase is the name of the container running KDB database container
	ContainerDatabase = "database"

	//ContainerSidecar = "mgr"
	//
	//ContainerInit = "init"
	//
	//ContainerMonitor = "monitor"
)

const (
	// PortDatabase is the name of a port that connects to kdb instance.
	PortDatabase = "database"
)
