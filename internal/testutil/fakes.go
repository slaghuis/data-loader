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

// FakeSink records EnsureTable / Truncate / WriteBatch / AtomicReplacer calls.
type FakeSink struct {
	mu sync.Mutex

	Exists        bool
	ExistsErr     error
	EnsureErr     error
	TruncateErr   error
	WriteErr      error
	BeginErr      error
	CommitErr     error
	AbortErr      error
	EnsureCalls   int
	TruncateCalls int
	WriteCalls    int
	BeginCalls    int
	CommitCalls   int
	AbortCalls    int
	RowsWritten   int64
	LastTarget    contracts.TargetTable
	LastWriteTo   contracts.TargetTable
	Truncated     bool
}

// Ensure FakeSink satisfies Sink and AtomicReplacer.
var (
	_ contracts.Sink           = (*FakeSink)(nil)
	_ contracts.AtomicReplacer = (*FakeSink)(nil)
)

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

func (s *FakeSink) WriteBatch(_ context.Context, target contracts.TargetTable, batch contracts.Batch, _ time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.WriteCalls++
	s.LastWriteTo = target
	if s.WriteErr != nil {
		return 0, s.WriteErr
	}
	n := int64(len(batch.Rows))
	s.RowsWritten += n
	return n, nil
}

func (s *FakeSink) BeginReplace(_ context.Context, target contracts.TargetTable, _ []contracts.Column) (contracts.TargetTable, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.BeginCalls++
	if s.BeginErr != nil {
		return contracts.TargetTable{}, s.BeginErr
	}
	stg := contracts.TargetTable{Schema: target.Schema, Name: target.Name + "_stg"}
	s.LastTarget = stg
	return stg, nil
}

func (s *FakeSink) CommitReplace(_ context.Context, target, _ contracts.TargetTable) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CommitCalls++
	s.LastTarget = target
	s.Exists = true
	return s.CommitErr
}

func (s *FakeSink) AbortReplace(context.Context, contracts.TargetTable) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AbortCalls++
	return s.AbortErr
}

// TruncateOnlySink implements Sink but not AtomicReplacer (legacy replace path).
type TruncateOnlySink struct {
	Exists        bool
	TruncateCalls int
	EnsureCalls   int
	WriteCalls    int
}

func (s *TruncateOnlySink) Open(context.Context) error { return nil }
func (s *TruncateOnlySink) Ping(context.Context) error { return nil }
func (s *TruncateOnlySink) Close() error               { return nil }
func (s *TruncateOnlySink) TableExists(context.Context, contracts.TargetTable) (bool, error) {
	return s.Exists, nil
}
func (s *TruncateOnlySink) EnsureTable(context.Context, contracts.TargetTable, []contracts.Column) error {
	s.EnsureCalls++
	s.Exists = true
	return nil
}
func (s *TruncateOnlySink) Truncate(context.Context, contracts.TargetTable) error {
	s.TruncateCalls++
	return nil
}
func (s *TruncateOnlySink) WriteBatch(_ context.Context, _ contracts.TargetTable, batch contracts.Batch, _ time.Time) (int64, error) {
	s.WriteCalls++
	return int64(len(batch.Rows)), nil
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
