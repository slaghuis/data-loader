package runstate_test

import (
	"testing"

	"github.com/slaghuis/data-loader/internal/runstate"
)

func TestSnapshot_SortedByLoadID_D11(t *testing.T) {
	r := runstate.New()
	r.Register(runstate.LoadState{LoadID: 30, LoadName: "c"})
	r.Register(runstate.LoadState{LoadID: 10, LoadName: "a"})
	r.Register(runstate.LoadState{LoadID: 20, LoadName: "b"})

	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("len = %d, want 3", len(snap))
	}
	want := []int64{10, 20, 30}
	for i, id := range want {
		if snap[i].LoadID != id {
			t.Fatalf("snap[%d].LoadID = %d, want %d", i, snap[i].LoadID, id)
		}
	}
}
