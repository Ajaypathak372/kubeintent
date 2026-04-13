package telemetry

import (
	"context"
	"strings"
	"time"

	platformv1alpha1 "github.com/ajaypathak/kubeintent/api/v1alpha1"
)

const (
	// Annotations that override the default PromQL templates per AppIntent.
	// The value is a PromQL expression; {{.Name}} is replaced with the
	// target workload name.
	AnnotationPromQLp99 = "kubeintent.io/promql-p99"
	AnnotationPromQLp50 = "kubeintent.io/promql-p50"
	AnnotationPromQLRPS = "kubeintent.io/promql-rps"

	defaultP99Template = `histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{service="{{.Name}}"}[5m])) by (le))`
	defaultP50Template = `histogram_quantile(0.50, sum(rate(http_request_duration_seconds_bucket{service="{{.Name}}"}[5m])) by (le))`
	defaultRPSTemplate = `sum(rate(http_requests_total{service="{{.Name}}"}[1m]))`
)

// TelemetryProvider reads live telemetry for a given AppIntent's target
// workload. Implementations are expected to be safe for concurrent use.
type TelemetryProvider interface {
	ObserveLatencyP99(ctx context.Context, intent *platformv1alpha1.AppIntent) (time.Duration, error)
	ObserveLatencyP50(ctx context.Context, intent *platformv1alpha1.AppIntent) (time.Duration, error)
	ObserveRPS(ctx context.Context, intent *platformv1alpha1.AppIntent) (float64, error)
}

// resolveQuery returns the PromQL expression for the given metric, checking
// the AppIntent's annotations for an override before falling back to the
// default template. {{.Name}} is replaced in both cases.
func resolveQuery(intent *platformv1alpha1.AppIntent, annotation, defaultTemplate string) string {
	tmpl := defaultTemplate
	if override, ok := intent.Annotations[annotation]; ok && strings.TrimSpace(override) != "" {
		tmpl = strings.TrimSpace(override)
	}
	return strings.ReplaceAll(tmpl, "{{.Name}}", intent.Spec.TargetRef.Name)
}
