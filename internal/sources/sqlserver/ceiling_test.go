package sqlserver

import (
	"testing"
	"time"

	"github.com/slaghuis/data-loader/pkg/contracts"
)

func TestFormatCellWatermark_Datetime(t *testing.T) {
	ts := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	got := formatCellWatermark(ts, contracts.WatermarkDatetime)
	if got != "2024-06-01T12:00:00Z" {
		t.Fatalf("got %q", got)
	}
}

func TestCompareCeilingCaughtUp(t *testing.T) {
	// Documents ADR-022: when last >= ceiling the Read path returns without querying.
	cmp, err := contracts.CompareWatermark(contracts.WatermarkNumeric, "100", "100")
	if err != nil || cmp < 0 {
		t.Fatalf("cmp=%d err=%v", cmp, err)
	}
	min, err := contracts.MinWatermark(contracts.WatermarkNumeric, "150", "100")
	if err != nil || min != "100" {
		t.Fatalf("min=%q err=%v", min, err)
	}
}
