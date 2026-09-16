package sources

import (
	"fmt"
	"sync"

	"github.com/slaghuis/data-loader/pkg/contracts"
)

var (
	mu        sync.RWMutex
	factories = map[string]contracts.SourceFactory{}
)

// Register makes a source module available under a kind name (e.g. "sqlserver").
func Register(kind string, f contracts.SourceFactory) {
	mu.Lock()
	defer mu.Unlock()
	factories[kind] = f
}

// Build instantiates a source by kind.
func Build(cfg contracts.SourceConfig) (contracts.Source, error) {
	mu.RLock()
	f, ok := factories[cfg.SourceType]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no source registered for kind %q", cfg.SourceType)
	}
	return f(cfg)
}
