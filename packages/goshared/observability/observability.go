// Package observability wires distributed tracing.
//
// In a system where one browser request fans out across the gateway and several
// services, a per-service log line tells you what a service did but not what the
// request did. A trace does, and it is the difference between "identity was
// slow" and "identity was slow because applications called it per row".
//
// Tracing is optional: when no collector is configured the package installs a
// no-op provider, so a developer running two services on a laptop is not forced
// to run a collector too.
package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Config describes how a service reports traces.
type Config struct {
	ServiceName string
	Environment string
	// Endpoint is an OTLP/HTTP collector, e.g. http://localhost:4318. Empty
	// disables tracing.
	Endpoint string
	// SampleRatio is the fraction of traces recorded when no parent decision
	// exists. Zero means "use the default": everything in development, 10% in
	// production, because a busy production system does not need every span to
	// answer the questions traces are good at.
	SampleRatio float64
}

// Shutdown flushes pending spans. Always call it — a process that exits without
// flushing loses the trace for the request that made it exit.
type Shutdown func(context.Context) error

// Init installs the global tracer provider and propagator.
//
// The returned shutdown is safe to call even when tracing is disabled.
func Init(ctx context.Context, cfg Config, logger *slog.Logger) (Shutdown, error) {
	// W3C trace context is set even when tracing is off, so a service still
	// forwards an incoming traceparent to the next hop rather than breaking a
	// trace that started upstream.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	if cfg.Endpoint == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		logger.Info("tracing disabled: no OTLP endpoint configured")
		return func(context.Context) error { return nil }, nil
	}

	// WithEndpointURL takes the full signal URL, not the collector's base. Given
	// a bare origin it posts to "/" and every export comes back 404 — which
	// shows up only as a log line, so tracing looks configured while nothing
	// arrives. Append the standard path when the caller gave us just an origin.
	endpoint := strings.TrimSuffix(cfg.Endpoint, "/")
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Path == "" {
		endpoint += "/v1/traces"
	}

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, fmt.Errorf("observability: create exporter: %w", err)
	}

	// NewSchemaless, not NewWithAttributes: resource.Default() carries whatever
	// schema URL the SDK was built against, and merging two resources with
	// different schema URLs is an error. Pinning our own would make an SDK
	// upgrade break every service at boot, which is a high price for an
	// attribute nothing reads.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(cfg.ServiceName),
		attribute.String("deployment.environment", cfg.Environment),
	))
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}

	ratio := cfg.SampleRatio
	if ratio == 0 {
		ratio = 1.0
		if cfg.Environment == "production" {
			ratio = 0.1
		}
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
		// ParentBased keeps a trace whole: once the gateway decides to sample a
		// request, every downstream service records its part of it.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)
	otel.SetTracerProvider(provider)

	logger.Info("tracing enabled",
		slog.String("endpoint", cfg.Endpoint),
		slog.Float64("sample_ratio", ratio))

	return func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return errors.Join(provider.ForceFlush(shutdownCtx), provider.Shutdown(shutdownCtx))
	}, nil
}

// Middleware traces inbound HTTP requests.
//
// The span is named by route pattern rather than by path, so `/v1/jobs/{id}`
// aggregates instead of producing one span name per job id.
func Middleware(serviceName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, serviceName,
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				if pattern := r.Pattern; pattern != "" {
					return pattern
				}
				return r.Method + " " + r.URL.Path
			}),
			// Health checks would otherwise dominate the trace volume.
			otelhttp.WithFilter(func(r *http.Request) bool {
				return r.URL.Path != "/healthz" && r.URL.Path != "/readyz"
			}),
		)
	}
}

// HTTPClient returns an HTTP client that propagates trace context.
//
// Service-to-service calls made with this client join the caller's trace rather
// than starting an orphan.
func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
}

// Tracer returns a named tracer for manual spans.
func Tracer(name string) trace.Tracer { return otel.Tracer(name) }

// Span starts a child span and returns it with the derived context.
//
//	ctx, span := observability.Span(ctx, "store.ListJobs")
//	defer span.End()
func Span(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("karmarat").Start(ctx, name, trace.WithAttributes(attrs...))
}

// RecordError marks the active span as failed.
func RecordError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
}
