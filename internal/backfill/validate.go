package backfill

import (
	"fmt"
	"strings"
	"time"

	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

// ValidateParent checks that a load may request historic backfill (ADR-022).
func ValidateParent(load metadata.Load, sourceKind string) error {
	if !load.BackfillRequired {
		return nil
	}
	if load.BackfillOfLoadID != nil {
		return fmt.Errorf("sibling load cannot set backfill_required")
	}
	kind := strings.ToLower(strings.TrimSpace(sourceKind))
	if kind != "sqlserver" && kind != "postgres" {
		return fmt.Errorf("backfill requires sqlserver or postgres source, got %q", sourceKind)
	}
	if load.Mode != string(contracts.LoadModeDelta) {
		return fmt.Errorf("backfill requires mode=delta, got %q", load.Mode)
	}
	if load.WatermarkColumn == "" || load.WatermarkType == "" || load.WatermarkType == string(contracts.WatermarkNone) {
		return fmt.Errorf("backfill requires watermark_column and watermark_type")
	}
	if stop := strings.TrimSpace(load.BackfillStop); stop != "" {
		if err := validateStop(contracts.WatermarkType(load.WatermarkType), stop); err != nil {
			return fmt.Errorf("backfill_stop: %w", err)
		}
	}
	return nil
}

func validateStop(typ contracts.WatermarkType, stop string) error {
	switch typ {
	case contracts.WatermarkDatetime:
		if _, err := time.Parse(time.RFC3339Nano, stop); err != nil {
			if _, err2 := time.Parse(time.RFC3339, stop); err2 != nil {
				return fmt.Errorf("must be RFC3339 datetime: %w", err)
			}
		}
		return nil
	case contracts.WatermarkNumeric:
		_, err := contracts.CompareWatermark(typ, stop, stop)
		return err
	default:
		return fmt.Errorf("unsupported watermark_type %q", typ)
	}
}

// SiblingName returns the unique name for a backfill sibling of parentName.
func SiblingName(parentName string) string {
	return parentName + "#backfill"
}
