package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	platformv1alpha1 "github.com/ajaypathak/kubeintent/api/v1alpha1"
)

// PrometheusClient is a thin wrapper around the Prometheus HTTP query API.
type PrometheusClient struct {
	api v1.API
}

// NewPrometheusClient creates a client pointing at the given Prometheus URL.
// The connection is lazy — no network call is made until Query is called.
func NewPrometheusClient(url string) (*PrometheusClient, error) {
	c, err := api.NewClient(api.Config{Address: url})
	if err != nil {
		return nil, fmt.Errorf("creating prometheus client: %w", err)
	}
	return &PrometheusClient{api: v1.NewAPI(c)}, nil
}

// Query runs an instant PromQL query and returns a single float64 result.
// It accepts both scalar results and single-element vectors (which is what
// histogram_quantile / sum(rate(...)) return). A 5-second timeout is applied.
func (c *PrometheusClient) Query(ctx context.Context, promql string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result, _, err := c.api.Query(ctx, promql, time.Now())
	if err != nil {
		return 0, fmt.Errorf("prometheus query %q: %w", promql, err)
	}

	switch v := result.(type) {
	case *model.Scalar:
		return float64(v.Value), nil
	case model.Vector:
		if len(v) == 0 {
			return 0, fmt.Errorf("prometheus query %q returned empty vector", promql)
		}
		if len(v) > 1 {
			return 0, fmt.Errorf("prometheus query %q returned %d samples, expected 1", promql, len(v))
		}
		return float64(v[0].Value), nil
	default:
		return 0, fmt.Errorf("prometheus query %q returned unexpected type %T", promql, result)
	}
}

// PrometheusProvider implements TelemetryProvider by querying a Prometheus
// server. Default PromQL templates assume the standard
// http_request_duration_seconds / http_requests_total naming convention;
// per-AppIntent overrides are available via annotations.
type PrometheusProvider struct {
	client *PrometheusClient
}

// NewPrometheusProvider returns a provider backed by the given client.
func NewPrometheusProvider(client *PrometheusClient) *PrometheusProvider {
	return &PrometheusProvider{client: client}
}

func (p *PrometheusProvider) ObserveLatencyP99(ctx context.Context, intent *platformv1alpha1.AppIntent) (time.Duration, error) {
	query := resolveQuery(intent, AnnotationPromQLp99, defaultP99Template)
	val, err := p.client.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("observing p99 latency: %w", err)
	}
	// Prometheus histogram buckets use seconds; convert to time.Duration.
	return time.Duration(val * float64(time.Second)), nil
}

func (p *PrometheusProvider) ObserveLatencyP50(ctx context.Context, intent *platformv1alpha1.AppIntent) (time.Duration, error) {
	query := resolveQuery(intent, AnnotationPromQLp50, defaultP50Template)
	val, err := p.client.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("observing p50 latency: %w", err)
	}
	return time.Duration(val * float64(time.Second)), nil
}

func (p *PrometheusProvider) ObserveRPS(ctx context.Context, intent *platformv1alpha1.AppIntent) (float64, error) {
	query := resolveQuery(intent, AnnotationPromQLRPS, defaultRPSTemplate)
	val, err := p.client.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("observing RPS: %w", err)
	}
	return val, nil
}
