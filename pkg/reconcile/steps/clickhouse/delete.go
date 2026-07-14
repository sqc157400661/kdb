package clickhouse

import (
	"fmt"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/pkg/reconcile/context"
)

type deletionAction string

const (
	deletionStopGateway       deletionAction = "StopGatewayConnections"
	deletionStopWrites        deletionAction = "StopWrites"
	deletionSuspendGroups     deletionAction = "SuspendGroups"
	deletionRemoveReplicated  deletionAction = "RemoveReplicatedDatabaseRegistrations"
	deletionRetainPVCs        deletionAction = "RetainPVCs"
	deletionRemoveKeeperLast  deletionAction = "RemoveKeeperMetadataLast"
)

func protectedDeleteAllowed(instance *v1.KDBInstance) error {
	if instance == nil {
		return nil
	}
	if instance.Annotations != nil && instance.Annotations[annotationDestructiveDeleteConfirmed] == "true" {
		return nil
	}
	return fmt.Errorf("unsafe ClickHouse deletion blocked; set %s=true after backup and explicit confirmation", annotationDestructiveDeleteConfirmed)
}

func orderedDeletionActions(instance *v1.KDBInstance) []deletionAction {
	actions := []deletionAction{
		deletionStopGateway,
		deletionStopWrites,
		deletionSuspendGroups,
		deletionRemoveReplicated,
		deletionRetainPVCs,
		deletionRemoveKeeperLast,
	}
	return actions
}

func prepareClickHouseDeletion(rc *context.InstanceContext) (bool, error) {
	if err := deleteGatewayResources(rc); err != nil {
		return false, err
	}
	return true, nil
}
