package topology

import (
	"fmt"
	"regexp"
	"strings"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/apis/shared"
	"github.com/sqc157400661/kdb/internal/naming"
)

const clickHouseReplicaChangeConfirmedAnnotation = "clickhouse.kdb.com/replica-change-confirmed"

var clickHouseComputeGroupNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidateClickHouseSpec validates ClickHouse topology without defaulting or mutating the input.
func ValidateClickHouseSpec(instance, oldInstance *v1.KDBInstance) error {
	if instance == nil || !naming.IsClickHouseEngine(instance) {
		return nil
	}
	spec := instance.Spec.ClickHouse
	if spec == nil {
		return fmt.Errorf("spec.clickhouse is required when engine is clickhouse")
	}
	if spec.DataShards < 1 {
		return fmt.Errorf("clickhouse dataShards must be greater than or equal to 1")
	}
	if oldInstance != nil && oldInstance.Spec.ClickHouse != nil && oldInstance.Spec.ClickHouse.DataShards != spec.DataShards {
		return fmt.Errorf("clickhouse dataShards is immutable; use NewInstance migration to change shard count")
	}
	if err := validateClickHouseKeeper(spec); err != nil {
		return err
	}
	if err := validateClickHouseComputeGroups(spec); err != nil {
		return err
	}
	if spec.Backup != nil && spec.Backup.Enabled != nil && *spec.Backup.Enabled &&
		(instance.Spec.Config == nil || strings.TrimSpace(instance.Spec.Config["clickhouse.backupRunner.image"]) == "") {
		return fmt.Errorf("clickhouse backup requires spec.config[clickhouse.backupRunner.image]")
	}
	if spec.Backup != nil && spec.Backup.Enabled != nil && *spec.Backup.Enabled &&
		(spec.Backup.ObjectStorageRef == nil || strings.TrimSpace(spec.Backup.ObjectStorageRef.Name) == "") {
		return fmt.Errorf("clickhouse backup requires objectStorageRef")
	}
	if spec.Gateway != nil && spec.Gateway.Enabled != nil && *spec.Gateway.Enabled {
		if spec.Gateway.BindingSecretRef == nil || strings.TrimSpace(spec.Gateway.BindingSecretRef.Name) == "" {
			return fmt.Errorf("clickhouse gateway requires bindingSecretRef")
		}
		if spec.Gateway.Replicas != nil && *spec.Gateway.Replicas < 1 {
			return fmt.Errorf("clickhouse gateway replicas must be greater than or equal to 1")
		}
		if instance.Spec.Config == nil || strings.TrimSpace(instance.Spec.Config["clickhouse.gateway.image"]) == "" {
			return fmt.Errorf("clickhouse gateway requires spec.config[clickhouse.gateway.image]")
		}
	}
	if oldInstance != nil && oldInstance.Spec.ClickHouse != nil {
		if err := validateClickHousePVCExpansion(spec, oldInstance.Spec.ClickHouse); err != nil {
			return err
		}
		if err := validateClickHouseReplicaChangeConfirmation(instance, oldInstance); err != nil {
			return err
		}
	}
	return nil
}

// ValidateClickHouseObservedStatus protects topology dimensions whose safe
// online migration is intentionally not exposed by the current controller.
func ValidateClickHouseObservedStatus(instance *v1.KDBInstance) error {
	if instance == nil || instance.Spec.ClickHouse == nil || instance.Status.ClickHouse == nil {
		return nil
	}
	if observed := instance.Status.ClickHouse.DataShards; observed > 0 && observed != instance.Spec.ClickHouse.DataShards {
		return fmt.Errorf("clickhouse dataShards is immutable; use NewInstance migration to change shard count")
	}
	if instance.Spec.ClickHouse.Keeper.Mode == v1.ClickHouseKeeperDedicated && instance.Status.ClickHouse.Keeper != nil && instance.Status.ClickHouse.Keeper.Members > 0 {
		desired := int32(3)
		if instance.Spec.ClickHouse.Keeper.Replicas != nil {
			desired = *instance.Spec.ClickHouse.Keeper.Replicas
		}
		if desired != instance.Status.ClickHouse.Keeper.Members {
			return fmt.Errorf("online ClickHouse Keeper replica changes are not exposed; create a migration plan before changing keeper.replicas")
		}
	}
	return nil
}

func validateClickHouseKeeper(spec *v1.ClickHouseSpec) error {
	switch spec.Keeper.Mode {
	case v1.ClickHouseKeeperDedicated:
		if spec.Keeper.Ref != nil {
			return fmt.Errorf("clickhouse keeper dedicated mode must not set ref")
		}
		if spec.Keeper.Replicas != nil && !isValidClickHouseKeeperReplicas(*spec.Keeper.Replicas) {
			return fmt.Errorf("clickhouse dedicated keeper replicas must be 1, 3, or 5")
		}
		if spec.Keeper.Instance == nil {
			return fmt.Errorf("clickhouse keeper dedicated mode requires instance")
		}
		if clickHouseProductionRequested(spec) && spec.Keeper.Replicas != nil && *spec.Keeper.Replicas == 1 {
			return fmt.Errorf("clickhouse production keeper replicas must not be 1")
		}
	case v1.ClickHouseKeeperSharedRef:
		if spec.Keeper.Ref == nil {
			return fmt.Errorf("clickhouse keeper sharedRef mode requires ref")
		}
		if spec.Keeper.Replicas != nil || spec.Keeper.Instance != nil {
			return fmt.Errorf("clickhouse keeper sharedRef mode must not set replicas or instance")
		}
	case "":
		return fmt.Errorf("clickhouse keeper mode is required")
	default:
		return fmt.Errorf("unsupported clickhouse keeper mode: %s", spec.Keeper.Mode)
	}
	return nil
}

func validateClickHouseComputeGroups(spec *v1.ClickHouseSpec) error {
	if len(spec.ComputeGroups) == 0 {
		return fmt.Errorf("clickhouse computeGroups must not be empty")
	}
	seen := map[string]struct{}{}
	seenResourceNames := map[string]string{}
	ingestCount := 0
	servingCount := 0
	for i, group := range spec.ComputeGroups {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			return fmt.Errorf("clickhouse computeGroups[%d].name is required", i)
		}
		if name != group.Name {
			return fmt.Errorf("clickhouse computeGroups[%d].name must not contain leading or trailing whitespace", i)
		}
		if !clickHouseComputeGroupNamePattern.MatchString(name) {
			return fmt.Errorf("clickhouse compute group name %q must match %s", name, clickHouseComputeGroupNamePattern.String())
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("clickhouse compute group names must be unique: %s", name)
		}
		seen[name] = struct{}{}
		resourceName := naming.ClickHouseGroupHeadlessServiceName("instance", name)
		if existing, ok := seenResourceNames[resourceName]; ok {
			return fmt.Errorf("clickhouse compute group names %s and %s normalize to the same Kubernetes resource name", existing, name)
		}
		seenResourceNames[resourceName] = name
		switch group.Role {
		case v1.ClickHouseRoleIngest:
			ingestCount++
			if group.Lifecycle != nil && group.Lifecycle.AutoSuspendEnabled != nil && *group.Lifecycle.AutoSuspendEnabled {
				return fmt.Errorf("clickhouse ingest compute group must not enable auto suspend")
			}
		case v1.ClickHouseRoleServing:
			servingCount++
			if group.Lifecycle != nil && group.Lifecycle.AutoSuspendEnabled != nil && *group.Lifecycle.AutoSuspendEnabled {
				return fmt.Errorf("clickhouse serving compute group must not enable auto suspend")
			}
		case v1.ClickHouseRoleAdhoc:
		default:
			return fmt.Errorf("unsupported clickhouse compute group role: %s", group.Role)
		}
		if group.Instance.Replicas != nil && *group.Instance.Replicas < 1 {
			return fmt.Errorf("clickhouse compute group %s replicasPerShard must be greater than or equal to 1", name)
		}
		if group.Lifecycle != nil && group.Lifecycle.WarmReplicasPerShard != nil {
			warm := *group.Lifecycle.WarmReplicasPerShard
			replicas := clickHouseReplicasPerShard(group)
			if warm < 0 || warm > replicas {
				return fmt.Errorf("clickhouse compute group %s warmReplicasPerShard must be between 0 and replicasPerShard", name)
			}
		}
	}
	if ingestCount != 1 {
		return fmt.Errorf("exactly one Ingest compute group is required")
	}
	if clickHouseProductionRequested(spec) && servingCount == 0 {
		return fmt.Errorf("clickhouse production profile requires at least one Serving compute group")
	}
	return nil
}

func validateClickHousePVCExpansion(spec, oldSpec *v1.ClickHouseSpec) error {
	oldGroups := map[string]shared.InstanceSetSpec{}
	for _, group := range oldSpec.ComputeGroups {
		oldGroups[group.Name] = group.Instance
	}
	for _, group := range spec.ComputeGroups {
		oldInstance, ok := oldGroups[group.Name]
		if !ok {
			continue
		}
		if err := validateClickHousePVCSpecExpansion("clickhouse compute group "+group.Name, group.Instance.DataVolumeClaimSpec, oldInstance.DataVolumeClaimSpec); err != nil {
			return err
		}
	}
	if spec.Keeper.Instance != nil && oldSpec.Keeper.Instance != nil {
		if err := validateClickHousePVCSpecExpansion("clickhouse keeper", spec.Keeper.Instance.DataVolumeClaimSpec, oldSpec.Keeper.Instance.DataVolumeClaimSpec); err != nil {
			return err
		}
	}
	return nil
}

func validateClickHousePVCSpecExpansion(owner string, spec, oldSpec shared.PVCSpec) error {
	if oldSpec.StorageClass != "" && spec.StorageClass != "" && oldSpec.StorageClass != spec.StorageClass {
		return fmt.Errorf("%s storageClass is immutable", owner)
	}
	if !oldSpec.Size.IsZero() && !spec.Size.IsZero() && spec.Size.Cmp(oldSpec.Size) < 0 {
		return fmt.Errorf("%s pvc size must not shrink", owner)
	}
	return nil
}

func validateClickHouseReplicaChangeConfirmation(instance, oldInstance *v1.KDBInstance) error {
	oldGroups := map[string]int32{}
	for _, group := range oldInstance.Spec.ClickHouse.ComputeGroups {
		oldGroups[group.Name] = clickHouseReplicasPerShard(group)
	}
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		oldReplicas, ok := oldGroups[group.Name]
		if !ok {
			continue
		}
		newReplicas := clickHouseReplicasPerShard(group)
		if oldReplicas == newReplicas {
			continue
		}
		expected := fmt.Sprintf("%s:%d->%d", group.Name, oldReplicas, newReplicas)
		if instance.Annotations == nil || instance.Annotations[clickHouseReplicaChangeConfirmedAnnotation] != expected {
			return fmt.Errorf("clickhouse replicasPerShard change for %s requires confirmation annotation %s=%s", group.Name, clickHouseReplicaChangeConfirmedAnnotation, expected)
		}
	}
	return nil
}

func clickHouseReplicasPerShard(group v1.ClickHouseComputeGroupSpec) int32 {
	if group.Instance.Replicas == nil {
		return 1
	}
	return *group.Instance.Replicas
}

func isValidClickHouseKeeperReplicas(replicas int32) bool {
	switch replicas {
	case 1, 3, 5:
		return true
	default:
		return false
	}
}

func clickHouseProductionRequested(spec *v1.ClickHouseSpec) bool {
	return spec.Gateway != nil && spec.Gateway.Enabled != nil && *spec.Gateway.Enabled
}
