package backfill

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/slaghuis/data-loader/internal/metadata"
)

type memStore struct {
	mu       sync.Mutex
	loads    map[int64]*metadata.Load
	byParent map[int64]int64
	wms      map[int64]string
	nextID   int64
}

func newMemStore() *memStore {
	return &memStore{
		loads:    map[int64]*metadata.Load{},
		byParent: map[int64]int64{},
		wms:      map[int64]string{},
		nextID:   1,
	}
}

func (m *memStore) ListBackfillParents(context.Context) ([]metadata.Load, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []metadata.Load
	for _, l := range m.loads {
		if l.BackfillRequired && l.BackfillOfLoadID == nil {
			cp := *l
			out = append(out, cp)
		}
	}
	return out, nil
}

func (m *memStore) GetSiblingByParent(_ context.Context, parentID int64) (*metadata.Load, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byParent[parentID]
	if !ok {
		return nil, nil
	}
	cp := *m.loads[id]
	return &cp, nil
}

func (m *memStore) CreateLoad(_ context.Context, load *metadata.Load) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	load.ID = m.nextID
	m.nextID++
	cp := *load
	m.loads[load.ID] = &cp
	if load.BackfillOfLoadID != nil {
		m.byParent[*load.BackfillOfLoadID] = load.ID
	}
	return nil
}

func (m *memStore) SaveLoad(_ context.Context, load *metadata.Load) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *load
	m.loads[load.ID] = &cp
	return nil
}

func (m *memStore) GetWatermark(_ context.Context, loadID int64) (*metadata.Watermark, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.wms[loadID]
	if !ok {
		return nil, nil
	}
	return &metadata.Watermark{LoadID: loadID, Value: v}, nil
}

func (m *memStore) UpsertWatermark(_ context.Context, loadID int64, newValue string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wms[loadID] = newValue
	return nil
}

func (m *memStore) GetLoad(_ context.Context, id int64) (*metadata.Load, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.loads[id]
	if !ok {
		return nil, fmt.Errorf("load %d not found", id)
	}
	cp := *l
	return &cp, nil
}

func TestCoordinator_CreateActivateComplete_ADR022(t *testing.T) {
	st := newMemStore()
	parent := &metadata.Load{
		ID:               1,
		Name:             "erp.orders",
		SourceID:         10,
		Mode:             "delta",
		ObjectName:       "dbo.Orders",
		WatermarkColumn:  "ModifiedAt",
		WatermarkType:    "numeric",
		BatchSize:        100,
		AutoCreateTable:  true,
		CronExpression:   "0 * * * *",
		IsEnabled:        true,
		BackfillRequired: true,
		BackfillStop:     "10",
		TargetSchema:     "dbo",
		TargetTable:      "orders",
		Source:           metadata.Source{ID: 10, Kind: "sqlserver", Name: "erp"},
	}
	st.loads[1] = parent
	st.nextID = 2

	c := NewCoordinator(st, nil, slog.Default())
	ctx := context.Background()

	// No parent WM yet → sibling created disabled, not activated.
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	sib, err := st.GetSiblingByParent(ctx, 1)
	if err != nil || sib == nil {
		t.Fatalf("sibling: %v %#v", err, sib)
	}
	if sib.IsEnabled || sib.BackfillCeiling != "" {
		t.Fatalf("expected disabled sibling without ceiling, got %+v", sib)
	}
	if sib.Name != "erp.orders#backfill" {
		t.Fatalf("name=%q", sib.Name)
	}
	if sib.CronExpression != "1 * * * *" {
		t.Fatalf("cron=%q want minute+1", sib.CronExpression)
	}

	// Parent first-run WM → activate.
	st.wms[1] = "100"
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	sib, _ = st.GetSiblingByParent(ctx, 1)
	if !sib.IsEnabled || sib.BackfillCeiling != "100" {
		t.Fatalf("activate: %+v", sib)
	}
	if st.wms[sib.ID] != "10" {
		t.Fatalf("sibling wm=%q want 10", st.wms[sib.ID])
	}

	// Idempotent sync.
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}

	// Sibling catches up → complete.
	st.wms[sib.ID] = "100"
	fresh, _ := st.GetLoad(ctx, sib.ID)
	if err := c.AfterSuccessfulRun(ctx, *fresh); err != nil {
		t.Fatal(err)
	}
	sib, _ = st.GetSiblingByParent(ctx, 1)
	if sib.IsEnabled {
		t.Fatal("sibling should be disabled")
	}
	p, _ := st.GetLoad(ctx, 1)
	if p.BackfillRequired || p.BackfillCompletedAt == nil {
		t.Fatalf("parent complete: %+v", p)
	}
}

func TestValidateParent_RejectsTelemetry(t *testing.T) {
	err := ValidateParent(metadata.Load{
		BackfillRequired: true,
		Mode:             "delta",
		WatermarkColumn:  "t",
		WatermarkType:    "datetime",
	}, "modbus")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOffsetCron_MinutePlusOne(t *testing.T) {
	got, err := OffsetCron("0 */2 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1 */2 * * *" {
		t.Fatalf("got %q", got)
	}
}
