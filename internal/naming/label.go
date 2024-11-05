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
)

const (
	MysqlLabelKeyInstanceName = "mysql.kdb.com/instance-name"

	MysqlLableKeyWorkingPodName = "mysql.kdb.com/working-pod-name"

	// restore from backup
	// mysql cluster to recover
	MysqlAnnoKeyRestoreClusterId = "mysql.kdb.com/restore-clusterid"
	// the point-in-time to recover
	MysqlAnnoKeyRestorePointInTime = "mysql.kdb.com/restore-unix-pit"

	// full backup cron expression
	MysqlAnnoKeyFullBackupCron = "mysql.kdb.com/fullbackup-cron"
	// incr backup cron expression
	MysqlAnnoKeyIncrBackupCron = "mysql.kdb.com/incrbackup-cron"
	// MySQL serverId config
	MySQLAnnoKeyServerId = "mysql.kdb.com/server-id"
	// MySQL master instance name
	MySQLAnnoKeyMasterInstanceName = "mysql.kdb.com/master-instance"
	// MySQL master sigma cluster
	MySQLAnnoKeyMasterSigmaCluster = "mysql.kdb.com/master-sigma-cluster"
)
