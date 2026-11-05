package metrics_test

import (
	"testing"
	"time"

	"github.com/slaghuis/data-loader/internal/metrics"
)

func TestObserveRun_DoesNotPanic(t *testing.T) {
	metrics.ObserveRun("erp.orders", "success", "dbo.erp_orders", 10, 10, time.Now().UTC().Add(-time.Second))
	metrics.ObserveOverlapSkip("erp.orders")
	metrics.ObserveBadQuality("crusher.telemetry", 2)
	metrics.ObserveWatermarkLag("erp.orders", time.Now().UTC().Add(-2*time.Minute).Format(time.RFC3339Nano), time.Now().UTC())
}
