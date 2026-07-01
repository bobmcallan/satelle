package verb_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/logfile"
	"github.com/bobmcallan/satelle/internal/oplog"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestOpLogMirrorsMutations: state-mutating verbs append to the flat operation
// log a read-only reviewer can scan — create, a tag reconciliation, and an
// estimate all land with the story id and the before/after detail (sty_be257fef).
func TestOpLogMirrorsMutations(t *testing.T) {
	wire(t)
	dir := t.TempDir()
	verb.SetOpLog(oplog.New(dir, logfile.Config{}))
	t.Cleanup(func() { verb.SetOpLog(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "tags": []string{"sprint:9"}}), &it)
	call(t, "story-set", map[string]any{"id": it.ID, "tags": []string{"sprint:9", "order:1"}})
	call(t, "story-estimate", map[string]any{"id": it.ID, "tokens": 1000})

	log, err := os.ReadFile(filepath.Join(dir, "logs", "operations.log"))
	if err != nil {
		t.Fatalf("operation log not written: %v", err)
	}
	s := string(log)
	for _, want := range []string{it.ID, "story-create", "story-set", "order:1", "story-estimate"} {
		if !strings.Contains(s, want) {
			t.Errorf("operation log missing %q so a reviewer could not verify it:\n%s", want, s)
		}
	}
}

// TestOpLogUnwiredIsSafe: with no log wired, mutations still succeed (the log is
// best-effort and nil-safe).
func TestOpLogUnwiredIsSafe(t *testing.T) {
	wire(t)
	verb.SetOpLog(nil)
	var it workitem.Item
	if json.Unmarshal(call(t, "story-create", map[string]any{"title": "x"}), &it); it.ID == "" {
		t.Error("create failed with no oplog wired")
	}
}
