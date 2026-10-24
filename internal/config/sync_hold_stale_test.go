package config

import (
	"os"
	"path/filepath"
	"testing"
)

// [sync] hold_stale_after is retired (sty_40853d02): hosted holds are gone and
// nothing reads it. A config still carrying it loads, the key is never minted as
// an area, and RetiredSyncKeyPresent is what lets `satelle doctor` name it.
func TestRetiredHoldStaleAfterStillLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "satelle.toml")
	if err := os.WriteFile(path, []byte("[sync]\nstale_after = \"24h\"\nhold_stale_after = \"2h\"\nstories = \"personal\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("a config carrying the retired key must load: %v", err)
	}
	if !RetiredSyncKeyPresent(cfg) {
		t.Error("the retired key is present and must be reported")
	}
	if sc, err := ScopeFor(cfg, "stories"); err != nil || sc == LocalScope {
		t.Errorf("stories scope = %v, %v; the retired key must not disturb the areas", sc, err)
	}
	if RetiredSyncKeyPresent(Config{Sync: SyncTable{"stale_after": "24h"}}) {
		t.Error("a config without the key reports it")
	}
	if !reservedSyncKeys[RetiredSyncHoldStaleAfterKey] {
		t.Error("hold_stale_after must stay a reserved [sync] key")
	}
	for _, a := range SyncAreas {
		if a == RetiredSyncHoldStaleAfterKey {
			t.Error("hold_stale_after minted as a sync area")
		}
	}
}
