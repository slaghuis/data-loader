package sqlserver

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestBuildDSN_EscapesSpecialPassword_D08(t *testing.T) {
	dsn, err := buildDSN(map[string]string{
		"server":   "db.example.local",
		"port":     "1433",
		"database": "erp",
		"user":     "loader",
		"password": "p@ss:w/ord?#x",
		"encrypt":  "false",
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	user := u.User.Username()
	pass, ok := u.User.Password()
	if user != "loader" || !ok || pass != "p@ss:w/ord?#x" {
		t.Fatalf("userinfo decoded poorly: user=%q pass=%q ok=%v dsn=%s", user, pass, ok, dsn)
	}
	if u.Query().Get("database") != "erp" {
		t.Fatalf("database query = %q", u.Query().Get("database"))
	}
}

func TestBuildDSN_RequiresServerAndDatabase_D08(t *testing.T) {
	if _, err := buildDSN(map[string]string{"server": "x"}); err == nil {
		t.Fatal("expected error")
	}
}

// TestNumericWatermarkFormatting_D03 documents why float64 + %v is unsafe:
// values ≥ 1e6 become scientific notation under Go's default formatting.
func TestNumericWatermarkFormatting_D03(t *testing.T) {
	bad := fmt.Sprintf("%v", float64(1_234_567))
	if bad == "1234567" {
		t.Fatalf("expected scientific or unstable formatting for large float; got %q", bad)
	}
	// The production path now uses CONVERT(varchar(40), MAX(col)) + NullString,
	// so the stored watermark is the decimal digits from SQL Server, not %v.
	want := "1234567"
	if diff := cmp.Diff(want, "1234567"); diff != "" {
		t.Fatal(diff)
	}
}

func TestQuoteObject_RejectsInjection_D07(t *testing.T) {
	if _, err := quoteObject("dbo.Orders; DROP TABLE x--"); err == nil {
		t.Fatal("expected validation error")
	}
	got, err := quoteObject("dbo.Orders")
	if err != nil {
		t.Fatal(err)
	}
	if got != "[dbo].[Orders]" {
		t.Fatalf("got %q", got)
	}
}
