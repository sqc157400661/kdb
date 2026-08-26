package naming

import (
	"crypto/sha256"
	"encoding/base32"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	LabelScopeTenant      = "database.platform.io/tenant"
	LabelScopeProject     = "database.platform.io/project"
	LabelScopeEnvironment = "database.platform.io/environment"
	LabelScopeRegion      = "database.platform.io/region"
	LabelScopeInstance    = "database.platform.io/instance"

	AnnotationRawTenant      = "database.platform.io/raw-tenant"
	AnnotationRawProject     = "database.platform.io/raw-project"
	AnnotationRawEnvironment = "database.platform.io/raw-environment"
	AnnotationRawRegion      = "database.platform.io/raw-region"
	AnnotationRawInstance    = "database.platform.io/raw-instance"
	AnnotationScopeRevision  = "database.platform.io/scope-revision"
	AnnotationScopeHash      = "database.platform.io/scope-hash"
)

var platformScopeLabelKeys = []string{
	LabelScopeTenant,
	LabelScopeProject,
	LabelScopeEnvironment,
	LabelScopeRegion,
	LabelScopeInstance,
}

var platformScopeAnnotationKeys = []string{
	AnnotationRawTenant,
	AnnotationRawProject,
	AnnotationRawEnvironment,
	AnnotationRawRegion,
	AnnotationRawInstance,
	AnnotationScopeRevision,
	AnnotationScopeHash,
}

func KubernetesLabelValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(validation.IsValidLabelValue(raw)) == 0 {
		return raw
	}
	sum := sha256.Sum256([]byte(raw))
	return "v-" + strings.ToLower(
		base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]),
	)
}

func PlatformScopeLabels(source map[string]string) map[string]string {
	return selectedStringMap(source, platformScopeLabelKeys)
}

func PlatformScopeAnnotations(source map[string]string) map[string]string {
	return selectedStringMap(source, platformScopeAnnotationKeys)
}

func selectedStringMap(source map[string]string, keys []string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value := strings.TrimSpace(source[key]); value != "" {
			out[key] = value
		}
	}
	return out
}
