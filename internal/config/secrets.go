package config

import (
	"os"
	"strings"
)

// ResolveSecrets walks the params map and replaces "$env:NAME" values
// with the corresponding environment variable value.
func ResolveSecrets(params map[string]string) map[string]string {
	out := make(map[string]string, len(params))
	for k, v := range params {
		if strings.HasPrefix(v, "$env:") {
			envName := strings.TrimPrefix(v, "$env:")
			out[k] = os.Getenv(envName)
		} else {
			out[k] = v
		}
	}
	return out
}