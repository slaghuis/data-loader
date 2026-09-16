package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type AppConfig struct {
	MetadataDSN string
	SinkDSN     string
	LogLevel    string
	LogFormat   string

	// Housekeeping
	HousekeepingEnabled       bool
	HousekeepingCron          string
	LogRetentionDays          int
	LoadRunRetentionDays      int
	HousekeepingBatchSize     int
	HousekeepingBatchPauseMs  int

	// Health server
	HealthEnabled  bool
    HealthAddr     string
}

func FromEnv() (*AppConfig, error) {
	c := &AppConfig{
		MetadataDSN: os.Getenv("LOADER_METADATA_DSN"),
		SinkDSN:     os.Getenv("LOADER_SINK_DSN"),
		LogLevel:    getEnvDefault("LOADER_LOG_LEVEL", "info"),
		LogFormat:   getEnvDefault("LOADER_LOG_FORMAT", "json"),

		HousekeepingEnabled:      getEnvBool("LOADER_HOUSEKEEPING_ENABLED", true),
		HousekeepingCron:         getEnvDefault("LOADER_HOUSEKEEPING_CRON", "0 2 * * *"), // 02:00 daily
		LogRetentionDays:         getEnvInt("LOADER_LOG_RETENTION_DAYS", 14),
		LoadRunRetentionDays:     getEnvInt("LOADER_LOAD_RUN_RETENTION_DAYS", 90),
		HousekeepingBatchSize:    getEnvInt("LOADER_HOUSEKEEPING_BATCH_SIZE", 5000),
		HousekeepingBatchPauseMs: getEnvInt("LOADER_HOUSEKEEPING_BATCH_PAUSE_MS", 100),
		HealthEnabled: 			  getEnvBool("LOADER_HEALTH_ENABLED", true),
		HealthAddr:    			  getEnvDefault("LOADER_HEALTH_ADDR", "127.0.0.1:9090"),
	}
	if c.MetadataDSN == "" {
		return nil, fmt.Errorf("LOADER_METADATA_DSN is required")
	}
	if c.SinkDSN == "" {
		return nil, fmt.Errorf("LOADER_SINK_DSN is required")
	}
	return c, nil
}

func getEnvBool(k string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func getEnvInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getEnvDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// ResolveParams parses a source ParamsJSON string, resolving $env:VAR references.
func ResolveParams(paramsJSON string) (map[string]string, error) {
	var raw map[string]string
	if err := json.Unmarshal([]byte(paramsJSON), &raw); err != nil {
		return nil, fmt.Errorf("invalid params JSON: %w", err)
	}
	resolved := make(map[string]string, len(raw))
	for k, v := range raw {
		if strings.HasPrefix(v, "$env:") {
			envKey := strings.TrimPrefix(v, "$env:")
			envVal := os.Getenv(envKey)
			if envVal == "" {
				return nil, fmt.Errorf("env var %q referenced by param %q is not set", envKey, k)
			}
			resolved[k] = envVal
		} else {
			resolved[k] = v
		}
	}
	return resolved, nil
}
