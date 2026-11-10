package contracts

import "testing"

func TestCompareWatermark_Numeric(t *testing.T) {
	cmp, err := CompareWatermark(WatermarkNumeric, "99", "100")
	if err != nil {
		t.Fatal(err)
	}
	if cmp >= 0 {
		t.Fatalf("cmp=%d want < 0", cmp)
	}
	min, err := MinWatermark(WatermarkNumeric, "100", "50")
	if err != nil || min != "50" {
		t.Fatalf("min=%q err=%v", min, err)
	}
}

func TestCompareWatermark_Datetime(t *testing.T) {
	a := "2024-01-01T00:00:00Z"
	b := "2024-06-01T00:00:00Z"
	cmp, err := CompareWatermark(WatermarkDatetime, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if cmp >= 0 {
		t.Fatalf("cmp=%d want < 0", cmp)
	}
}

func TestCompareWatermark_EmptySortsLow(t *testing.T) {
	cmp, err := CompareWatermark(WatermarkNumeric, "", "1")
	if err != nil {
		t.Fatal(err)
	}
	if cmp >= 0 {
		t.Fatalf("cmp=%d want < 0", cmp)
	}
}
