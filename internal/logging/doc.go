// Package logging configures slog and mirrors records asynchronously into
// meta_log_entries through a bounded buffer (ADR-008). Overflow increments
// Dropped() / loader_dblog_dropped_total; console output is never blocked.
package logging
