package assets

import "embed"

//go:embed prometheus-operator-v0.92.0-bundle.yaml
var FS embed.FS

const PrometheusOperatorV0920Bundle = "prometheus-operator-v0.92.0-bundle.yaml"
