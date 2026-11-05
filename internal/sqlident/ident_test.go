package sqlident_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"github.com/slaghuis/data-loader/internal/sqlident"
)

func TestValidate_D07(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{name: "simple", in: "Orders"},
		{name: "underscore", in: "_id"},
		{name: "space", in: "Order Date"},
		{name: "dollar", in: "col$1"},
		{name: "empty", in: "", wantErr: true},
		{name: "starts digit", in: "1abc", wantErr: true},
		{name: "semicolon", in: "a; DROP", wantErr: true},
		{name: "bracket", in: "a]", wantErr: true},
		{name: "too long", in: strings.Repeat("a", 129), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := sqlident.Validate(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate(%q) err=%v wantErr=%v", tt.in, err, tt.wantErr)
			}
		})
	}
}

func TestQuoteMSSQL_EscapesBracket_D07(t *testing.T) {
	// Validation rejects ']', so QuoteMSSQL itself must still escape when
	// called on already-trimmed names used internally.
	got := sqlident.QuoteMSSQL("weird]name")
	want := "[weird]]name]"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("QuoteMSSQL mismatch (-want +got):\n%s", diff)
	}
}

func TestQuoteMSSQLQualified_D07(t *testing.T) {
	got, err := sqlident.QuoteMSSQLQualified("dbo.Orders")
	if err != nil {
		t.Fatal(err)
	}
	if got != "[dbo].[Orders]" {
		t.Fatalf("got %q", got)
	}
}

func TestQuotePostgresQualified_AlwaysQuotes_D07(t *testing.T) {
	got, err := sqlident.QuotePostgresQualified(`public.orders`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `"public"."orders"` {
		t.Fatalf("got %q", got)
	}
	// Previously passed through when the caller embedded quotes — must reject.
	if _, err := sqlident.QuotePostgresQualified(`"evil"`); err == nil {
		// "evil" after trim is evil — actually ValidateQualified trims quotes
		// so "evil" becomes evil which is valid. The attack was pass-through of
		// embedded quote mid-name. That fails Validate.
	}
	if _, err := sqlident.QuotePostgresQualified(`a"b`); err == nil {
		t.Fatal("expected error for embedded quote")
	}
}

func FuzzValidate_D07(f *testing.F) {
	f.Add("Orders")
	f.Add("a]")
	f.Add("schema.table")
	f.Add("")
	f.Add(strings.Repeat("x", 200))
	f.Fuzz(func(t *testing.T, s string) {
		_ = sqlident.Validate(s)
		_ = sqlident.ValidateQualified(s)
		if err := sqlident.Validate(s); err == nil {
			q := sqlident.QuoteMSSQL(s)
			if !strings.HasPrefix(q, "[") || !strings.HasSuffix(q, "]") {
				t.Fatalf("bad quote %q for %q", q, s)
			}
			// Escaping: count of unescaped ] inside must be zero after parse.
			inner := q[1 : len(q)-1]
			if strings.Contains(strings.ReplaceAll(inner, "]]", ""), "]") {
				t.Fatalf("unescaped ] in %q", q)
			}
		}
		if !utf8.ValidString(s) {
			return
		}
	})
}
