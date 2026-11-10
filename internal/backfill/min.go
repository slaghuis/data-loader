package backfill

import (
	"context"
	"fmt"

	"github.com/slaghuis/data-loader/internal/config"
	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

// MinWatermarker is implemented by SQL sources that can resolve MIN(column).
type MinWatermarker interface {
	CurrentMinWatermark(ctx context.Context, objectName, column string, typ contracts.WatermarkType) (contracts.Watermark, error)
}

// SourceMinResolver opens a source briefly to resolve MIN for an empty backfill_stop.
type SourceMinResolver struct {
	Build     func(cfg contracts.SourceConfig) (contracts.Source, error)
	Telemetry contracts.TelemetryCatalog
}

// ResolveMin implements MinResolver.
func (r *SourceMinResolver) ResolveMin(ctx context.Context, load metadata.Load) (string, error) {
	if r == nil || r.Build == nil {
		return "", fmt.Errorf("SourceMinResolver not configured")
	}
	params, err := config.ResolveParams(load.Source.ParamsJSON)
	if err != nil {
		return "", err
	}
	src, err := r.Build(contracts.SourceConfig{
		SourceID:   load.SourceID,
		SourceType: load.Source.Kind,
		Name:       load.Source.Name,
		Params:     params,
		Telemetry:  r.Telemetry,
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()
	if err := src.Open(ctx); err != nil {
		return "", err
	}
	mw, ok := src.(MinWatermarker)
	if !ok {
		return "", fmt.Errorf("source kind %q does not support MIN watermark", load.Source.Kind)
	}
	wm, err := mw.CurrentMinWatermark(ctx, load.ObjectName, load.WatermarkColumn, contracts.WatermarkType(load.WatermarkType))
	if err != nil {
		return "", err
	}
	if wm.Value == "" {
		return "", fmt.Errorf("source table has no rows for MIN(%s)", load.WatermarkColumn)
	}
	return wm.Value, nil
}

// MinFunc adapts a function to MinResolver.
type MinFunc func(ctx context.Context, load metadata.Load) (string, error)

// ResolveMin implements MinResolver.
func (f MinFunc) ResolveMin(ctx context.Context, load metadata.Load) (string, error) {
	return f(ctx, load)
}
