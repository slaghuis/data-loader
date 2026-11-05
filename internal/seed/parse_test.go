package seed_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/slaghuis/data-loader/internal/seed"
)

// TestParseAllSeeds_D06 ensures every sample under seeds/ parses with KnownFields
// (rejects obsolete keys such as enabled: instead of is_enabled:).
func TestParseAllSeeds_D06(t *testing.T) {
	root := filepath.Join("..", "..", "seeds")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read seeds dir: %v", err)
	}
	var found int
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		found++
		path := filepath.Join(root, e.Name())
		f, err := seed.Load(path)
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		if len(f.Sources) == 0 {
			t.Errorf("%s: expected at least one source", e.Name())
		}
	}
	if found == 0 {
		t.Fatal("no seed yaml files found")
	}
}
