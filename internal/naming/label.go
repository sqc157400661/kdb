package naming

const labelPrefix = "kdb."
const (
	// LabelCluster et al. provides the fundamental labels for Postgres instances
	LabelCluster     = labelPrefix + "clusterID"
	LabelInstance    = labelPrefix + "instance"
	LabelInstanceSet = labelPrefix + "instanceSet"

	LabelMasterHostname = labelPrefix + "masterHostname"
	LabelMasterIP       = labelPrefix + "masterIP"

	LabelHaProxy = labelPrefix + "ha"

	LabelRole = labelPrefix + "role"

	// LabelData is used to identify Pods and Volumes data store KDB data.
	LabelData = labelPrefix + "data"
	// LabelLog is used to identify Pods and Volumes log store KDB data.
	LabelLog = labelPrefix + "log"
)

const (
	MysqlLabelKeyInstanceName = "kdb.com/instance-name"

	MysqlLableKeyWorkingPodName = "kdb.com/working-pod-name"

	// restore from backup
	// mysql cluster to recover
	MysqlAnnoKeyRestoreClusterId = "kdb.com/restore-clusterid"
	// the point-in-time to recover
	MysqlAnnoKeyRestorePointInTime = "kdb.com/restore-unix-pit"

	// full backup cron expression
	MysqlAnnoKeyFullBackupCron = "kdb.com/fullbackup-cron"
	// incr backup cron expression
	MysqlAnnoKeyIncrBackupCron = "kdb.com/incrbackup-cron"
	// MySQL serverId config
	MySQLAnnoKeyServerId = "kdb.com/server-id"
	// MySQL master instance name
	MySQLAnnoKeyMasterInstanceName = "kdb.com/master-instance"
	// MySQL master sigma cluster
	MySQLAnnoKeyMasterSigmaCluster = "kdb.com/master-sigma-cluster"
)
