package contracts

import "testing"

func TestLowerBoundWithOverlap_ADR023(t *testing.T) {
	last := "2024-06-01T12:00:00Z"
	got, err := LowerBoundWithOverlap(last, WatermarkDatetime, 3600)
	if err != nil {
		t.Fatal(err)
	}
	want := "2024-06-01T11:00:00Z"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestLowerBoundWithOverlap_ZeroUnchanged(t *testing.T) {
	last := "2024-06-01T12:00:00Z"
	got, err := LowerBoundWithOverlap(last, WatermarkDatetime, 0)
	if err != nil || got != last {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestValidateWatermarkOverlap(t *testing.T) {
	if err := ValidateWatermarkOverlap("delta", WatermarkDatetime, 60, false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWatermarkOverlap("delta", WatermarkNumeric, 60, false); err == nil {
		t.Fatal("expected numeric reject")
	}
	if err := ValidateWatermarkOverlap("append", WatermarkDatetime, 60, false); err == nil {
		t.Fatal("expected append reject")
	}
	if err := ValidateWatermarkOverlap("delta", WatermarkDatetime, -1, false); err == nil {
		t.Fatal("expected negative reject")
	}
	if err := ValidateWatermarkOverlap("delta", WatermarkDatetime, MaxWatermarkOverlapSec+1, false); err == nil {
		t.Fatal("expected max reject")
	}
	if err := ValidateWatermarkOverlap("delta", WatermarkDatetime, 60, true); err == nil {
		t.Fatal("expected sibling reject")
	}
}

func TestEffectiveOverlapSec_CeilingDisables(t *testing.T) {
	if got := EffectiveOverlapSec(300, "ceiling"); got != 0 {
		t.Fatalf("got %d", got)
	}
	if got := EffectiveOverlapSec(300, ""); got != 300 {
		t.Fatalf("got %d", got)
	}
}
