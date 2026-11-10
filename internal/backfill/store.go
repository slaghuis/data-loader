package backfill

import (
	"context"

	"github.com/slaghuis/data-loader/internal/metadata"
)

// Store is the metadata surface the coordinator needs (ADR-022).
type Store interface {
	ListBackfillParents(ctx context.Context) ([]metadata.Load, error)
	GetSiblingByParent(ctx context.Context, parentID int64) (*metadata.Load, error)
	CreateLoad(ctx context.Context, load *metadata.Load) error
	SaveLoad(ctx context.Context, load *metadata.Load) error
	GetWatermark(ctx context.Context, loadID int64) (*metadata.Watermark, error)
	UpsertWatermark(ctx context.Context, loadID int64, newValue string) error
	GetLoad(ctx context.Context, id int64) (*metadata.Load, error)
}

// MinResolver resolves MIN(watermark_column) when backfill_stop is empty.
type MinResolver interface {
	ResolveMin(ctx context.Context, load metadata.Load) (string, error)
}
