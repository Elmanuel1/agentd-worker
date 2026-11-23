package dispatch

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/metric"
)

type metrics struct {
	launches    metric.Int64Counter
	collections metric.Int64Counter
	timeouts    metric.Int64Counter
	releases    metric.Int64Counter
}

func newMetrics(meter metric.Meter, queue Queue) (metrics, error) {
	var m metrics
	var errs [5]error
	m.launches, errs[0] = meter.Int64Counter("launches", metric.WithDescription("sandboxes launched for an item"))
	m.collections, errs[1] = meter.Int64Counter("collections", metric.WithDescription("items collected by their sandbox"))
	m.timeouts, errs[2] = meter.Int64Counter("handoff_timeouts", metric.WithDescription("items not collected within the handoff wait"))
	m.releases, errs[3] = meter.Int64Counter("releases", metric.WithDescription("sandboxes released for a gone session"))
	_, errs[4] = meter.Int64ObservableGauge("queue_depth",
		metric.WithDescription("items waiting in the environment's queue"),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			depth, err := queue.Depth(ctx)
			if err != nil {
				return err
			}
			o.Observe(depth)
			return nil
		}))
	return m, errors.Join(errs[:]...)
}
