package pipeline

import (
	"context"

	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

// RunStore is the narrow metadata surface the runner needs.
type RunStore interface {
	GetWatermark(ctx context.Context, loadID int64) (*metadata.Watermark, error)
	UpsertWatermark(ctx context.Context, loadID int64, newValue string) error
	StartRun(ctx context.Context, loadID int64, watermarkStart string) (*metadata.LoadRun, error)
	FinishRun(ctx context.Context, run *metadata.LoadRun, status string, rowsRead, rowsWritten int64, watermarkEnd, errMsg string) error
}

// SourceBuilder constructs a Source from a resolved SourceConfig.
type SourceBuilder func(cfg contracts.SourceConfig) (contracts.Source, error)
