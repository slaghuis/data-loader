package contracts

import (
	"fmt"
	"math/big"
	"strings"
	"time"
)

// CompareWatermark returns -1 if a < b, 0 if a == b, 1 if a > b.
// Empty values sort before any non-empty value.
func CompareWatermark(typ WatermarkType, a, b string) (int, error) {
	if a == b {
		return 0, nil
	}
	if a == "" {
		return -1, nil
	}
	if b == "" {
		return 1, nil
	}
	switch typ {
	case WatermarkDatetime:
		ta, err := time.Parse(time.RFC3339Nano, a)
		if err != nil {
			return 0, fmt.Errorf("parse watermark a: %w", err)
		}
		tb, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			return 0, fmt.Errorf("parse watermark b: %w", err)
		}
		switch {
		case ta.Before(tb):
			return -1, nil
		case ta.After(tb):
			return 1, nil
		default:
			return 0, nil
		}
	case WatermarkNumeric:
		ra, ok := new(big.Rat).SetString(strings.TrimSpace(a))
		if !ok {
			return 0, fmt.Errorf("parse numeric watermark a %q", a)
		}
		rb, ok := new(big.Rat).SetString(strings.TrimSpace(b))
		if !ok {
			return 0, fmt.Errorf("parse numeric watermark b %q", b)
		}
		return ra.Cmp(rb), nil
	default:
		switch {
		case a < b:
			return -1, nil
		case a > b:
			return 1, nil
		default:
			return 0, nil
		}
	}
}

// MinWatermark returns the lesser of a and b under typ.
func MinWatermark(typ WatermarkType, a, b string) (string, error) {
	cmp, err := CompareWatermark(typ, a, b)
	if err != nil {
		return "", err
	}
	if cmp <= 0 {
		return a, nil
	}
	return b, nil
}
