package contracts

import (
	"fmt"
	"time"
)

// MaxWatermarkOverlapSec is the largest allowed overlap (7 days) per ADR-023.
const MaxWatermarkOverlapSec = 7 * 24 * 60 * 60

// ValidateWatermarkOverlap checks watermark_overlap_sec against ADR-023 rules.
// isBackfillSibling should be true for ADR-022 sibling loads (overlap must be 0).
func ValidateWatermarkOverlap(mode string, watermarkType WatermarkType, overlapSec int, isBackfillSibling bool) error {
	if overlapSec < 0 {
		return fmt.Errorf("watermark_overlap_sec must be >= 0")
	}
	if overlapSec > MaxWatermarkOverlapSec {
		return fmt.Errorf("watermark_overlap_sec must be <= %d (7 days)", MaxWatermarkOverlapSec)
	}
	if overlapSec == 0 {
		return nil
	}
	if isBackfillSibling {
		return fmt.Errorf("watermark_overlap_sec must be 0 on backfill sibling loads")
	}
	if mode != string(LoadModeDelta) {
		return fmt.Errorf("watermark_overlap_sec requires mode=delta")
	}
	if watermarkType != WatermarkDatetime {
		return fmt.Errorf("watermark_overlap_sec requires watermark_type=datetime")
	}
	return nil
}

// EffectiveOverlapSec returns the overlap to apply on a read (0 when ceiling-capped).
func EffectiveOverlapSec(overlapSec int, watermarkCeiling string) int {
	if overlapSec <= 0 || watermarkCeiling != "" {
		return 0
	}
	return overlapSec
}

// LowerBoundWithOverlap returns the query lower-bound watermark string after
// subtracting overlapSec from a datetime last watermark (ADR-023).
// When overlapSec is 0, last is returned unchanged.
func LowerBoundWithOverlap(last string, typ WatermarkType, overlapSec int) (string, error) {
	if overlapSec <= 0 || last == "" {
		return last, nil
	}
	if typ != WatermarkDatetime {
		return last, nil
	}
	t, err := time.Parse(time.RFC3339Nano, last)
	if err != nil {
		t, err = time.Parse(time.RFC3339, last)
		if err != nil {
			return "", fmt.Errorf("parse watermark for overlap: %w", err)
		}
	}
	return t.UTC().Add(-time.Duration(overlapSec) * time.Second).Format(time.RFC3339Nano), nil
}
