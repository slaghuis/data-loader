package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// AppConfig is the loader's own bootstrap configuration.
type AppConfig struct {
	MetadataDSN string // MS SQL DSN for the metadata DB
	SinkDSN     string // MS SQL DSN for the sink DB (can be the same server, different DB)
	LogLevel    string
}

func FromEnv() (*AppConfig, error) {
	c := &AppConfig{
		MetadataDSN: os.Getenv("LOADER_METADATA_DSN"),
		SinkDSN:     os.Getenv("LOADER_SINK_DSN"),
		LogLevel:    getEnvDefault("LOADER_LOG_LEVEL", "info"),
	}
	if c.MetadataDSN == "" {
		return nil, fmt.Errorf("LOADER_METADATA_DSN is required")
	}
	if c.SinkDSN == "" {
		return nil, fmt.Errorf("LOADER_SINK_DSN is required")
	}
	return c, nil
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