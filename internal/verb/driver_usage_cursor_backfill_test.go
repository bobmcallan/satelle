package verb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// TestBackfilledCursorRowCarriesTheSubagentQualifier pins AC4 on the backfill path:
// cursor's windowed read is available, but satelle records only stop hooks, so the
// backfilled row must carry the same qualifier a live cursor row does and never pass
// for an unqualified measurement.
func TestBackfilledCursorRowCarriesTheSubagentQualifier(t *testing.T) {
	db := wireBackfill(t)
	useBackfillRepo(t)
	usage := t.TempDir()
	t.Setenv("SATELLE_CURSOR_USAGE_DIR", usage)
	line, err := json.Marshal(map[string]any{
		"at": bfAt("10:05"), "generation_id": "g1", "model": "composer-2.5",
		"input_tokens": 25758, "output_tokens": 266, "cache_read_tokens": 16563, "cache_write_tokens": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(usage, bfSession+".jsonl"), append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	id := bfStoryOn(t, db, "cursor story", agentcli.HarnessCursor, "cursor: no driver-usage reader for this harness", bfAt("10:00"), bfAt("10:10"))

	if rep := bfBackfill(t, id); len(rep.Sessions) != 1 || rep.Sessions[0].Status != BackfillWritten {
		t.Fatalf("report = %+v, want one backfilled cursor session", rep)
	}
	rows := backfilledRows(t, db, id)
	if len(rows) != 1 || !rows[0].Available || rows[0].Executable != agentcli.HarnessCursor {
		t.Fatalf("rows = %+v, want one available backfilled cursor row", rows)
	}
	const want = "cursor: subagent use undeterminable from recorded hooks; subagent spend inclusion unverified"
	if rows[0].AttributionReason != want {
		t.Fatalf("AttributionReason = %q, want %q", rows[0].AttributionReason, want)
	}
}
