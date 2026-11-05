package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/slaghuis/data-loader/pkg/contracts"
)

// SourceSession obtains an opened Source for a run and releases it afterwards.
// Ephemeral sessions close on release; pooled sessions may retain the connection (ADR-021).
type SourceSession interface {
	Obtain(ctx context.Context, cfg contracts.SourceConfig) (src contracts.Source, release func(invalidate bool), err error)
	// Close releases all retained connections (no-op for ephemeral).
	Close() error
	// PingAll probes retained connections; ephemeral returns nil.
	PingAll(ctx context.Context) error
}

// Ephemeral opens a fresh source per Obtain and closes it on release.
type Ephemeral struct {
	Build SourceBuilder
}

// NewEphemeral returns the default open-per-run session manager.
func NewEphemeral(build SourceBuilder) *Ephemeral {
	if build == nil {
		build = func(contracts.SourceConfig) (contracts.Source, error) {
			return nil, fmt.Errorf("source builder not configured")
		}
	}
	return &Ephemeral{Build: build}
}

func (e *Ephemeral) Obtain(ctx context.Context, cfg contracts.SourceConfig) (contracts.Source, func(bool), error) {
	src, err := e.Build(cfg)
	if err != nil {
		return nil, nil, err
	}
	if err := src.Open(ctx); err != nil {
		_ = src.Close()
		return nil, nil, err
	}
	release := func(bool) {
		_ = src.Close()
	}
	return src, release, nil
}

func (e *Ephemeral) Close() error                  { return nil }
func (e *Ephemeral) PingAll(context.Context) error { return nil }

// ParamsFingerprint digests kind + resolved params for pool identity (ADR-021).
func ParamsFingerprint(kind string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	_, _ = h.Write([]byte(strings.ToLower(kind)))
	_, _ = h.Write([]byte{0})
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(params[k]))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
