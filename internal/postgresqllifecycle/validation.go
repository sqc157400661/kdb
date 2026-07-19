package postgresqllifecycle

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
)

var digestImage = regexp.MustCompile(`^.+@sha256:[0-9a-f]{64}$`)

var extensionMatrix = map[string]map[string]bool{
	"16": {"contrib": true, "postgis": true, "pgvector": true, "pg_stat_statements": true, "pg_repack": true},
	"17": {"contrib": true, "postgis": true, "pgvector": true, "pg_stat_statements": true, "pg_repack": true},
	"18": {"contrib": true, "postgis": true, "pgvector": true, "pg_stat_statements": true, "pg_repack": true},
}

func ValidateUpdate(oldInstance, instance *v1.KDBInstance) error {
	if instance == nil || instance.Spec.PostgreSQL == nil {
		return nil
	}
	if err := validateReplicas(instance); err != nil {
		return err
	}
	if oldInstance != nil && oldInstance.Spec.InstanceSet.Replicas != nil && instance.Spec.InstanceSet.Replicas != nil &&
		*instance.Spec.InstanceSet.Replicas < *oldInstance.Spec.InstanceSet.Replicas && oldInstance.Status.PostgreSQL != nil && oldInstance.Status.PostgreSQL.Primary == "" {
		return fmt.Errorf("cannot scale down while the PostgreSQL primary is unknown")
	}
	if oldInstance != nil && instance.Spec.InstanceSet.DataVolumeClaimSpec.Size.Cmp(oldInstance.Spec.InstanceSet.DataVolumeClaimSpec.Size) < 0 {
		return fmt.Errorf("PostgreSQL data PVC size cannot decrease")
	}
	if err := validateExtensions(instance.Spec.EngineVersion, instance.Spec.PostgreSQL.Lifecycle); err != nil {
		return err
	}
	return validateUpgrade(instance)
}

func validateReplicas(instance *v1.KDBInstance) error {
	if instance.Spec.InstanceSet.Replicas == nil {
		return fmt.Errorf("PostgreSQL replicas are required")
	}
	desired := *instance.Spec.InstanceSet.Replicas
	profile := "single"
	if instance.Spec.PostgreSQL.HA != nil && instance.Spec.PostgreSQL.HA.Profile != "" {
		profile = instance.Spec.PostgreSQL.HA.Profile
	}
	if profile == "single" {
		if desired != 1 {
			return fmt.Errorf("single PostgreSQL profile requires exactly 1 member")
		}
		return nil
	}
	minimum := int32(3)
	if instance.Spec.PostgreSQL.HA != nil && instance.Spec.PostgreSQL.HA.SynchronousNodeCount != nil {
		if candidate := *instance.Spec.PostgreSQL.HA.SynchronousNodeCount + 2; candidate > minimum {
			minimum = candidate
		}
	}
	if desired < minimum {
		return fmt.Errorf("PostgreSQL %s requires at least %d members to preserve primary and synchronous replica safety", profile, minimum)
	}
	return nil
}

func validateUpgrade(instance *v1.KDBInstance) error {
	lifecycle := instance.Spec.PostgreSQL.Lifecycle
	if lifecycle == nil || lifecycle.Upgrade == nil {
		return nil
	}
	upgrade := lifecycle.Upgrade
	if strings.TrimSpace(upgrade.OperationID) == "" || strings.TrimSpace(upgrade.TargetImage) == "" {
		return fmt.Errorf("PostgreSQL upgrade requires operationId and targetImage")
	}
	current, err := strconv.Atoi(instance.Spec.EngineVersion)
	if err != nil || current < 16 || current > 18 {
		return fmt.Errorf("unsupported current PostgreSQL major %q", instance.Spec.EngineVersion)
	}
	switch upgrade.Type {
	case "Minor":
		if upgrade.TargetMajorVersion != "" && upgrade.TargetMajorVersion != instance.Spec.EngineVersion {
			return fmt.Errorf("minor upgrade cannot change PostgreSQL major version")
		}
	case "Major":
		target, err := strconv.Atoi(upgrade.TargetMajorVersion)
		if err == nil && target == current && instance.Status.PostgreSQL != nil && instance.Status.PostgreSQL.Lifecycle != nil &&
			instance.Status.PostgreSQL.Lifecycle.OperationID == upgrade.OperationID {
			phase := instance.Status.PostgreSQL.Lifecycle.Phase
			if phase == "Upgrading" || phase == "RebuildingReplicas" || phase == "Succeeded" {
				return nil
			}
		}
		if err != nil || target != current+1 || target > 18 {
			return fmt.Errorf("major upgrade must target the next supported major version")
		}
		if upgrade.MaintenanceWindow == "" || upgrade.RecoverableBackupRef == "" || !upgrade.ExtensionsCompatible || !upgrade.AcceptIrreversible {
			return fmt.Errorf("major upgrade requires maintenance window, recoverable backup, extension compatibility and irreversible-change acceptance")
		}
		if err := validateExtensions(upgrade.TargetMajorVersion, lifecycle); err != nil {
			return fmt.Errorf("target extension compatibility: %w", err)
		}
	default:
		return fmt.Errorf("unsupported PostgreSQL upgrade type %q", upgrade.Type)
	}
	return nil
}

func validateExtensions(major string, lifecycle *v1.PostgreSQLLifecycleSpec) error {
	if lifecycle == nil {
		return nil
	}
	allowed, ok := extensionMatrix[major]
	if !ok {
		return fmt.Errorf("unsupported PostgreSQL extension major %q", major)
	}
	seen := map[string]bool{}
	for _, extension := range lifecycle.Extensions {
		name := strings.ToLower(strings.TrimSpace(extension.Name))
		if name == "" || seen[name] {
			return fmt.Errorf("extension names must be non-empty and unique")
		}
		seen[name] = true
		if extension.CustomImage == nil {
			if !allowed[name] {
				return fmt.Errorf("extension %q is not in the PostgreSQL %s allowlist", name, major)
			}
			continue
		}
		image := extension.CustomImage
		if !digestImage.MatchString(image.Image) || !image.SignatureVerified || image.ScanStatus != "Passed" || strings.TrimSpace(image.AttestationRef) == "" {
			return fmt.Errorf("custom extension %q requires immutable digest, verified signature, passed scan and attestation", name)
		}
	}
	return nil
}

func ExtensionNames(major string) []string {
	result := make([]string, 0, len(extensionMatrix[major]))
	for name := range extensionMatrix[major] {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
