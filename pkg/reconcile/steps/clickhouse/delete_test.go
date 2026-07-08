package clickhouse

import "testing"

func TestProtectedDeleteRequiresConfirmation(t *testing.T) {
	instance := replicatedInstance()
	if err := protectedDeleteAllowed(instance); err == nil {
		t.Fatalf("expected destructive delete to be blocked")
	}
	instance.Annotations = map[string]string{annotationDestructiveDeleteConfirmed: "true"}
	if err := protectedDeleteAllowed(instance); err != nil {
		t.Fatalf("expected confirmed delete to pass: %v", err)
	}
}

func TestOrderedDeletionActionsRetainPVCsAndKeeperLast(t *testing.T) {
	actions := orderedDeletionActions(replicatedInstance())
	if len(actions) == 0 {
		t.Fatalf("expected deletion actions")
	}
	if actions[len(actions)-1] != deletionRemoveKeeperLast {
		t.Fatalf("expected keeper cleanup last, got %#v", actions)
	}
	hasRetainPVC := false
	for _, action := range actions {
		if action == deletionRetainPVCs {
			hasRetainPVC = true
		}
	}
	if !hasRetainPVC {
		t.Fatalf("expected PVC retention action")
	}
}
