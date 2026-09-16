package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"time"

	"gorm.io/gorm"

	"github.com/slaghuis/data-loader/internal/runstate"
)

// Server exposes /healthz, /readyz, /status.
type Server struct {
	addr    string
	logger  *slog.Logger
	state   *runstate.Registry
	metaDB  *gorm.DB
	sinkDB  *sql.DB
	version string

	srv *http.Server
}

// New builds the server. Any of metaDB/sinkDB may be nil to skip that check.
func New(addr string, state *runstate.Registry, metaDB *gorm.DB, sinkDB *sql.DB, version string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if version == "" {
		version = "dev"
	}
	return &Server{
		addr:    addr,
		logger:  logger,
		state:   state,
		metaDB:  metaDB,
		sinkDB:  sinkDB,
		version: version,
	}
}

// Start binds and begins serving. Non-blocking. Errors from ListenAndServe are logged.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/status", s.handleStatus)

	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("health server error", "category", "health", "err", err)
		}
	}()

	s.logger.Info("health server listening", "category", "health", "addr", s.addr)
	return nil
}

// Stop gracefully drains in-flight requests.
func (s *Server) Stop(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// ---- handlers ----

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"uptime_seconds": int64(time.Since(s.state.StartedAt()).Seconds()),
	})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	result := map[string]any{
		"status":   "ok",
		"metadata": "ok",
		"sink":     "ok",
	}
	overall := http.StatusOK

	if s.metaDB != nil {
		if sqlDB, err := s.metaDB.DB(); err != nil {
			result["metadata"] = err.Error()
			result["status"] = "degraded"
			overall = http.StatusServiceUnavailable
		} else if err := sqlDB.PingContext(ctx); err != nil {
			result["metadata"] = err.Error()
			result["status"] = "degraded"
			overall = http.StatusServiceUnavailable
		}
	}

	if s.sinkDB != nil {
		if err := s.sinkDB.PingContext(ctx); err != nil {
			result["sink"] = err.Error()
			result["status"] = "degraded"
			overall = http.StatusServiceUnavailable
		}
	}

	writeJSON(w, overall, result)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	loads := s.state.Snapshot()

	// Roll up counts for a quick-glance summary.
	var running, succeeded, failed int
	for _, l := range loads {
		if l.CurrentlyRunning {
			running++
		}
		switch l.LastStatus {
		case "success":
			succeeded++
		case "failed":
			failed++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":        s.version,
		"go_version":     runtime.Version(),
		"started_at":     s.state.StartedAt(),
		"uptime_seconds": int64(time.Since(s.state.StartedAt()).Seconds()),
		"summary": map[string]any{
			"total_loads":  len(loads),
			"running":      running,
			"last_success": succeeded,
			"last_failed":  failed,
		},
		"loads": loads,
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}