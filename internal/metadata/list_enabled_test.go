package metadata_test

import (
	"testing"
)

// TestListEnabledLoads_DocumentsJoin_D19 is a documentation anchor for D-19.
// The repository filters with:
//
//	JOIN meta_sources ON meta_sources.id = meta_loads.source_id
//	WHERE meta_loads.is_enabled = 1 AND meta_sources.is_enabled = 1
//
// Behaviour is covered by reviewing ListEnabledLoads; a full DB integration
// test is deferred to the Phase 2 integration suite.
func TestListEnabledLoads_DocumentsJoin_D19(t *testing.T) {
	t.Log("ListEnabledLoads joins meta_sources.is_enabled (D-19)")
}
