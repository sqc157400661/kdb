# 2026-06-20 KDBInstance NotFound Patch Handling

## Goal

Prevent the KDB operator from treating deferred KDBInstance patch attempts as reconcile failures when the CR has already disappeared.

## Task List

- [x] Inspect the operator error stack from `PatchKDBInstanceStatus`.
- [x] Review `InstanceContext` patch paths used by deferred reconcile steps.
- [x] Ignore Kubernetes `NotFound` errors from status and object patch operations.
- [x] Add regression tests for missing KDBInstance during deferred patch.
- [x] Run the relevant Go checks.
- [ ] Push the fix branch and prepare the merge path into `develop`.

## Notes

- The operator log showed `kdbinstances.kdb.com "mysql-sqc-766000" not found` from a deferred status patch.
- This can happen when a reconcile loaded the CR and it was deleted before deferred patch execution.
- The fix keeps other errors visible while making missing CR patches no-op.
- `go test ./...` still exposes pre-existing `internal/security` assertions unrelated to this patch.
