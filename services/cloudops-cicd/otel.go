package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// initTracer sets up OTLP HTTP export. Empty OTEL_EXPORTER_OTLP_ENDPOINT disables export (no-op).
func initTracer(ctx context.Context) (func(context.Context) error, error) {
	endpoint := strings.TrimSpace(env("OTEL_EXPORTER_OTLP_ENDPOINT", ""))
	if endpoint == "" {
		logJSON("info", "otel_disabled", map[string]any{"reason": "OTEL_EXPORTER_OTLP_ENDPOINT empty"})
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, fmt.Errorf("otlp http exporter: %w", err)
	}

	svcName := env("OTEL_SERVICE_NAME", appName)
	svcVersion := env("SERVICE_VERSION", version)
	deployEnv := env("DEPLOYMENT_ENVIRONMENT", "dev")
	deployID := env("DEPLOYMENT_ID", "unknown")

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(svcName),
			semconv.ServiceVersion(svcVersion),
			attribute.String("deployment.environment", deployEnv),
			attribute.String("deployment.id", deployID),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(2*time.Second),
		),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logJSON("info", "otel_enabled", map[string]any{
		"endpoint":               endpoint,
		"service.name":           svcName,
		"service.version":        svcVersion,
		"deployment.environment": deployEnv,
		"deployment.id":          deployID,
	})

	return tp.Shutdown, nil
}
