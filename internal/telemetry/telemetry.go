// Package telemetry sets up the OTLP metric exporter. The backend is chosen by the standard
// OTEL_* variables; with no endpoint set, metrics go nowhere.
package telemetry

import (
	"context"
	"os"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Setup returns a meter for service and a shutdown that flushes it.
func Setup(ctx context.Context, service string) (metric.Meter, func(context.Context) error, error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") == "" {
		return noop.Meter{}, func(context.Context) error { return nil }, nil
	}
	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(semconv.ServiceName(service)))
	if err != nil {
		return nil, nil, err
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(30*time.Second))),
	)
	return provider.Meter("github.com/Elmanuel1/agentd-worker"), provider.Shutdown, nil
}
