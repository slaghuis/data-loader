package backfill

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/robfig/cron/v3"
)

// OffsetCron returns a cron expression delayed by one minute relative to expr.
// Descriptor forms (@every, @hourly, …) are returned unchanged; sibling
// activation after the parent first-run already provides a natural stagger.
func OffsetCron(expr string) (string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", fmt.Errorf("empty cron expression")
	}
	if strings.HasPrefix(expr, "@") {
		if _, err := cron.ParseStandard(expr); err != nil {
			return "", fmt.Errorf("parse cron: %w", err)
		}
		return expr, nil
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return "", fmt.Errorf("expected 5-field cron, got %d fields", len(fields))
	}
	if _, err := cron.ParseStandard(expr); err != nil {
		return "", fmt.Errorf("parse cron: %w", err)
	}
	minField := fields[0]
	if strings.ContainsAny(minField, "*/,") {
		// Complex minute specs: keep as-is (best-effort stagger via activation).
		return expr, nil
	}
	min, err := strconv.Atoi(minField)
	if err != nil || min < 0 || min > 59 {
		return expr, nil
	}
	fields[0] = strconv.Itoa((min + 1) % 60)
	out := strings.Join(fields, " ")
	if _, err := cron.ParseStandard(out); err != nil {
		return "", fmt.Errorf("offset cron invalid: %w", err)
	}
	return out, nil
}
