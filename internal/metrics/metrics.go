// Package metrics registers Prometheus collectors for the data-loader process.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	RunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "loader_runs_total",
		Help: "Completed load runs by status.",
	}, []string{"load", "status"})

	RunDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "loader_run_duration_seconds",
		Help:    "Wall-clock duration of a load run.",
		Buckets: prometheus.DefBuckets,
	}, []string{"load"})

	RowsRead = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "loader_rows_read_total",
		Help: "Rows read from sources.",
	}, []string{"load"})

	RowsWritten = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "loader_rows_written_total",
		Help: "Rows written to sink targets.",
	}, []string{"load", "target"})

	LastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "loader_last_success_timestamp_seconds",
		Help: "Unix timestamp of the last successful run per load.",
	}, []string{"load"})

	OverlapSkips = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "loader_overlap_skips_total",
		Help: "Runs skipped because the previous run was still in progress.",
	}, []string{"load"})

	TelemetryBadQuality = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "loader_telemetry_bad_quality_total",
		Help: "Telemetry readings emitted with quality=bad.",
	}, []string{"load"})

	WatermarkLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "loader_watermark_lag_seconds",
		Help: "Seconds between now and the persisted datetime watermark (0 when unknown).",
	}, []string{"load"})
)

// RegisterDBLogDropped exposes loader_dblog_dropped_total via a CounterFunc over Dropped().
func RegisterDBLogDropped(dropped func() uint64) {
	prometheus.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: "loader_dblog_dropped_total",
		Help: "DB log entries dropped because the async buffer was full.",
	}, func() float64 {
		if dropped == nil {
			return 0
		}
		return float64(dropped())
	}))
}

// ObserveRun records end-of-run counters and gauges.
func ObserveRun(loadName, status, target string, rowsRead, rowsWritten int64, startedAt time.Time) {
	label := loadLabel(loadName)
	RunsTotal.WithLabelValues(label, status).Inc()
	RunDuration.WithLabelValues(label).Observe(time.Since(startedAt).Seconds())
	if rowsRead > 0 {
		RowsRead.WithLabelValues(label).Add(float64(rowsRead))
	}
	if rowsWritten > 0 {
		RowsWritten.WithLabelValues(label, target).Add(float64(rowsWritten))
	}
	if status == "success" {
		LastSuccess.WithLabelValues(label).Set(float64(time.Now().UTC().Unix()))
	}
}

// ObserveOverlapSkip increments the overlap-skip counter for a load.
func ObserveOverlapSkip(loadName string) {
	OverlapSkips.WithLabelValues(loadLabel(loadName)).Inc()
}

// ObserveBadQuality increments the bad-quality counter.
func ObserveBadQuality(loadName string, n int) {
	if n <= 0 {
		return
	}
	TelemetryBadQuality.WithLabelValues(loadLabel(loadName)).Add(float64(n))
}

// ObserveWatermarkLag sets lag for a datetime watermark value (RFC3339Nano).
func ObserveWatermarkLag(loadName, watermarkValue string, now time.Time) {
	label := loadLabel(loadName)
	if watermarkValue == "" {
		WatermarkLag.WithLabelValues(label).Set(0)
		return
	}
	ts, err := time.Parse(time.RFC3339Nano, watermarkValue)
	if err != nil {
		WatermarkLag.WithLabelValues(label).Set(0)
		return
	}
	lag := now.UTC().Sub(ts.UTC()).Seconds()
	if lag < 0 {
		lag = 0
	}
	WatermarkLag.WithLabelValues(label).Set(lag)
}

func loadLabel(name string) string {
	if name == "" {
		return "unknown"
	}
	return name
}
