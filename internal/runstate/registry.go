package runstate

import (
	"sync"
	"time"
)

// LoadState captures the most recent execution outcome for a single load.
type LoadState struct {
	LoadID          int64     `json:"load_id"`
	LoadName        string    `json:"load_name"`
	SourceName      string    `json:"source_name"`
	SourceKind      string    `json:"source_kind"`

	CronExpression  string    `json:"cron_expression"`

	LastRunUUID     string    `json:"last_run_uuid,omitempty"`
	LastStartedAt   time.Time `json:"last_started_at,omitempty"`
	LastFinishedAt  time.Time `json:"last_finished_at,omitempty"`
	LastStatus      string    `json:"last_status,omitempty"`        // success | failed | running
	LastRowsRead    int64     `json:"last_rows_read"`
	LastRowsWritten int64     `json:"last_rows_written"`
	LastError       string    `json:"last_error,omitempty"`

	CurrentlyRunning bool     `json:"currently_running"`
}

// Registry is a thread-safe in-memory store of load states.
type Registry struct {
	mu     sync.RWMutex
	loads  map[int64]*LoadState
	startedAt time.Time
}

func New() *Registry {
	return &Registry{
		loads:     map[int64]*LoadState{},
		startedAt: time.Now().UTC(),
	}
}

// StartedAt returns when the registry (and effectively the process) started.
func (r *Registry) StartedAt() time.Time { return r.startedAt }

// Register initialises state for a load without recording a run.
// Called at startup once schedules are known.
func (r *Registry) Register(s LoadState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Preserve any prior LastRun* fields if we're re-registering.
	if existing, ok := r.loads[s.LoadID]; ok {
		s.LastRunUUID = existing.LastRunUUID
		s.LastStartedAt = existing.LastStartedAt
		s.LastFinishedAt = existing.LastFinishedAt
		s.LastStatus = existing.LastStatus
		s.LastRowsRead = existing.LastRowsRead
		s.LastRowsWritten = existing.LastRowsWritten
		s.LastError = existing.LastError
		s.CurrentlyRunning = existing.CurrentlyRunning
	}
	r.loads[s.LoadID] = &s
}

// MarkRunning records that a run just started.
func (r *Registry) MarkRunning(loadID int64, runUUID string, startedAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.loads[loadID]
	if !ok {
		return
	}
	s.CurrentlyRunning = true
	s.LastRunUUID = runUUID
	s.LastStartedAt = startedAt
	s.LastStatus = "running"
	s.LastError = ""
}

// MarkFinished records the outcome of a run.
func (r *Registry) MarkFinished(loadID int64, status string, finishedAt time.Time, rowsRead, rowsWritten int64, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.loads[loadID]
	if !ok {
		return
	}
	s.CurrentlyRunning = false
	s.LastFinishedAt = finishedAt
	s.LastStatus = status
	s.LastRowsRead = rowsRead
	s.LastRowsWritten = rowsWritten
	s.LastError = errMsg
}

// Snapshot returns a copy of all load states, sorted for stable output.
func (r *Registry) Snapshot() []LoadState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]LoadState, 0, len(r.loads))
	for _, s := range r.loads {
		out = append(out, *s)
	}
	return out
}