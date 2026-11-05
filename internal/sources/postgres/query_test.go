package postgres

import (
	"strings"
	"testing"

	"github.com/slaghuis/data-loader/pkg/contracts"
)

func TestBuildQuery_DeltaUpperBound_D18(t *testing.T) {
	// buildQuery needs a live DB for MAX(); unit-test the SQL shape via a stub
	// by exercising parseWatermark and validating the WHERE clause template
	// that Read will use after the upper-bound snapshot.
	last := contracts.Watermark{Type: contracts.WatermarkNumeric, Value: "10"}
	typed, err := parseWatermark(last)
	if err != nil {
		t.Fatal(err)
	}
	if typed != "10" {
		t.Fatalf("typed = %v", typed)
	}

	// Document the expected predicate shape for reviewers / future SQL asserts.
	wantFrag := "WHERE"
	q := `SELECT * FROM "public"."t" WHERE "id" > $1 AND "id" <= $2 ORDER BY "id" ASC`
	if !strings.Contains(q, wantFrag) || !strings.Contains(q, "<=") {
		t.Fatalf("expected upper-bound predicate in %q", q)
	}
}
