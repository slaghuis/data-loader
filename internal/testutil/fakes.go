package testutil

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

// FakeSource is a hand-written Source for unit tests.
type FakeSource struct {
	KindName string
	OpenErr  error
	PingErr  error
	CloseErr error
	ReadFn   func(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error)

	OpenCalls  int
	CloseCalls int
}

func (f *FakeSource) Kind() string { return f.KindName }

func (f *FakeSource) Open(context.Context) error {
	f.OpenCalls++
	return f.OpenErr
}

func (f *FakeSource) Ping(context.Context) error { return f.PingErr }

func (f *FakeSource) Close() error {
	f.CloseCalls++
	return f.CloseErr
}

func (f *FakeSource) Read(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error) {
	if f.ReadFn != nil {
		return f.ReadFn(ctx, load, handler)
	}
	return contracts.ReadResult{}, nil
}

// FakeSink records EnsureTable / Truncate / WriteBatch calls.
type FakeSink struct {
	mu sync.Mutex

	Exists        bool
	ExistsErr     error
	EnsureErr     error
	TruncateErr   error
	WriteErr      error
	EnsureCalls   int
	TruncateCalls int
	WriteCalls    int
	RowsWritten   int64
	LastTarget    contracts.TargetTable
	Truncated     bool
}

func (s *FakeSink) Open(context.Context) error { return nil }
func (s *FakeSink) Ping(context.Context) error { return nil }
func (s *FakeSink) Close() error               { return nil }

func (s *FakeSink) TableExists(context.Context, contracts.TargetTable) (bool, error) {
	return s.Exists, s.ExistsErr
}

func (s *FakeSink) EnsureTable(_ context.Context, target contracts.TargetTable, _ []contracts.Column) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.EnsureCalls++
	s.LastTarget = target
	s.Exists = true
	return s.EnsureErr
}

func (s *FakeSink) Truncate(_ context.Context, target contracts.TargetTable) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TruncateCalls++
	s.LastTarget = target
	s.Truncated = true
	return s.TruncateErr
}

func (s *FakeSink) WriteBatch(_ context.Context, _ contracts.TargetTable, batch contracts.Batch, _ time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.WriteCalls++
	if s.WriteErr != nil {
		return 0, s.WriteErr
	}
	n := int64(len(batch.Rows))
	s.RowsWritten += n
	return n, nil
}

// FakeRunStore is an in-memory metadata.Repository stand-in for pipeline tests.
type FakeRunStore struct {
	mu sync.Mutex

	Watermark      *metadata.Watermark
	GetWMErr       error
	UpsertErr      error
	StartErr       error
	FinishErr      error
	Runs           []*metadata.LoadRun
	FinishedStatus []string
	UpsertCalls    int
	UpsertValues   []string
}

func (s *FakeRunStore) GetWatermark(context.Context, int64) (*metadata.Watermark, error) {
	return s.Watermark, s.GetWMErr
}

func (s *FakeRunStore) UpsertWatermark(_ context.Context, _ int64, newValue string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UpsertCalls++
	s.UpsertValues = append(s.UpsertValues, newValue)
	if s.UpsertErr != nil {
		return s.UpsertErr
	}
	s.Watermark = &metadata.Watermark{Value: newValue}
	return nil
}

func (s *FakeRunStore) StartRun(_ context.Context, loadID int64, watermarkStart string) (*metadata.LoadRun, error) {
	if s.StartErr != nil {
		return nil, s.StartErr
	}
	run := &metadata.LoadRun{
		ID:             int64(len(s.Runs) + 1),
		RunUUID:        fmt.Sprintf("run-%d", len(s.Runs)+1),
		LoadID:         loadID,
		Status:         "running",
		StartedAt:      time.Now().UTC(),
		WatermarkStart: watermarkStart,
	}
	s.mu.Lock()
	s.Runs = append(s.Runs, run)
	s.mu.Unlock()
	return run, nil
}

func (s *FakeRunStore) FinishRun(_ context.Context, run *metadata.LoadRun, status string, rowsRead, rowsWritten int64, watermarkEnd, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FinishedStatus = append(s.FinishedStatus, status)
	run.Status = status
	run.RowsRead = rowsRead
	run.RowsWritten = rowsWritten
	run.WatermarkEnd = watermarkEnd
	run.ErrorMessage = errMsg
	now := time.Now().UTC()
	run.FinishedAt = &now
	return s.FinishErr
}
