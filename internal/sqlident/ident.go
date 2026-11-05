package sqlident

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxIdentLen is the maximum accepted identifier length (SQL Server limit is 128).
const MaxIdentLen = 128

// identRE matches a single unquoted identifier segment.
// See Phase 2 defect D-07.
var identRE = regexp.MustCompile(`^[\p{L}_][\p{L}\p{N}_@$# .\-]{0,127}$`)

// Validate checks a single identifier segment (no schema.table dots).
func Validate(name string) error {
	if name == "" {
		return fmt.Errorf("identifier is empty")
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("identifier is not valid UTF-8")
	}
	if len(name) > MaxIdentLen {
		return fmt.Errorf("identifier %q exceeds %d characters", name, MaxIdentLen)
	}
	if !identRE.MatchString(name) {
		return fmt.Errorf("identifier %q contains illegal characters", name)
	}
	return nil
}

// ValidateQualified splits on '.' and validates each segment.
func ValidateQualified(name string) error {
	if name == "" {
		return fmt.Errorf("identifier is empty")
	}
	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return fmt.Errorf("identifier %q has too many segments", name)
	}
	for _, p := range parts {
		p = strings.Trim(p, "[]\"")
		if err := Validate(p); err != nil {
			return err
		}
	}
	return nil
}

// QuoteMSSQL wraps a single identifier in brackets, escaping embedded ']'.
func QuoteMSSQL(name string) string {
	name = strings.Trim(name, "[]")
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

// QuoteMSSQLQualified quotes "schema.table" or "table".
func QuoteMSSQLQualified(name string) (string, error) {
	if err := ValidateQualified(name); err != nil {
		return "", err
	}
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = QuoteMSSQL(strings.Trim(p, "[]"))
	}
	return strings.Join(parts, "."), nil
}

// QuotePostgres double-quotes a single identifier, escaping embedded quotes.
func QuotePostgres(name string) string {
	name = strings.Trim(name, `"`)
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// QuotePostgresQualified quotes "schema.table" or "table". Always quotes; never
// passes through raw input that already contains quotes (D-07).
func QuotePostgresQualified(name string) (string, error) {
	if err := ValidateQualified(name); err != nil {
		return "", err
	}
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = QuotePostgres(strings.Trim(p, `"`))
	}
	return strings.Join(parts, "."), nil
}
