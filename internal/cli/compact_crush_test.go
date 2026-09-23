package cli

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/retrieve"
)

const crushTestTOML = `
[output]
compact_commands = ["ledger-list"]
[output.crush]
enabled = true
min_items = 5
size_threshold_bytes = %d
max_kept = 10
first_fraction = 0.3
last_fraction = 0.15
variance_sigma = 2.0
structural_outlier_fraction = 0.2
rare_status_fraction = 0.2
status_fields = ["kind"]
error_keywords = ["error", "panic"]
`

// seedCrushLedger appends n note rows to a fresh story, row `bad` reporting a
// panic; returns the story id.
func seedCrushLedger(t *testing.T, n, bad int) string {
	t.Helper()
	id := createStory(t, "crush fixture")
	for i := 0; i < n; i++ {
		body := "routine entry"
		if i == bad {
			body = "panic: nil pointer"
		}
		if out, err := runRoot(t, "ledger", "append", "--story", id, "--kind", "note", "--body", body); err != nil {
			t.Fatalf("ledger append: %v\n%s", err, out)
		}
	}
	return id
}

// TestCompactCrushKeepsErrorAndRetrieves (sty_aa34491d AC3/AC4): over the
// size budget a marked command's list is crushed, the error row survives, and
// the trailing marker resolves through `satelle retrieve` to the full array.
func TestCompactCrushKeepsErrorAndRetrieves(t *testing.T) {
	repo := tempRepo(t)
	appendOutputConfig(t, repo, fmt.Sprintf(crushTestTOML, 500))
	id := seedCrushLedger(t, 60, 31)

	plain, err := runRoot(t, "ledger", "list", "--story", id, "--kind", "note", "--json")
	if err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "ledger", "list", "--story", id, "--kind", "note", "--compact")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) >= len(plain) {
		t.Fatalf("crushed output %d not smaller than plain %d", len(out), len(plain))
	}
	if !strings.Contains(out, "panic: nil pointer") {
		t.Errorf("error row dropped:\n%s", out)
	}
	hashes := retrieve.FindHashes(out)
	if len(hashes) != 1 || !strings.Contains(out, `"_crushed"`) {
		t.Fatalf("want one trailing crush marker, got hashes %v:\n%s", hashes, out)
	}
	got, err := runRoot(t, "retrieve", hashes[0])
	if err != nil {
		t.Fatal(err)
	}
	var want, have []map[string]any
	if err := json.Unmarshal([]byte(plain), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(got), &have); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, have) {
		t.Errorf("retrieved array differs from original (%d vs %d rows)", len(have), len(want))
	}
}

// TestCompactCrushScope (AC4): a list still under the size threshold after the
// lossless fold is not crushed.
func TestCompactCrushScope(t *testing.T) {
	repo := tempRepo(t)
	appendOutputConfig(t, repo, fmt.Sprintf(crushTestTOML, 10_000_000))
	id := seedCrushLedger(t, 30, 7)
	out, err := runRoot(t, "ledger", "list", "--story", id, "--kind", "note", "--compact")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "_crushed") || !isCompactTable(out) {
		t.Errorf("under threshold must stay the lossless table:\n%.300s", out)
	}
}

// TestCompactCrushMinItems (AC4): a list below min_items is never crushed even
// when over the size threshold.
func TestCompactCrushMinItems(t *testing.T) {
	repo := tempRepo(t)
	appendOutputConfig(t, repo, fmt.Sprintf(crushTestTOML, 1))
	id := seedCrushLedger(t, 4, 2)
	out, err := runRoot(t, "ledger", "list", "--story", id, "--kind", "note", "--compact")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "_crushed") {
		t.Errorf("4-row list crushed with min_items 5:\n%s", out)
	}
}

// TestCompactCrushSkipsUnmarkedVerb (AC4): a verb outside compact_commands is
// never crushed, whatever its size.
func TestCompactCrushSkipsUnmarkedVerb(t *testing.T) {
	repo := tempRepo(t)
	appendOutputConfig(t, repo, strings.Replace(fmt.Sprintf(crushTestTOML, 1), `["ledger-list"]`, `["story-list"]`, 1))
	id := seedCrushLedger(t, 30, 7)
	out, err := runRoot(t, "ledger", "list", "--story", id, "--kind", "note", "--compact")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "_crushed") {
		t.Errorf("unmarked verb crushed:\n%.300s", out)
	}
}
