package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// TestLedgerListRendersCacheSplitAndByteCounts pins sty_363eaf55 AC4: `satelle
// ledger list --story <id>` shows the fresh/cache-write/cache-read split and
// the system-prompt/payload byte counts an agent_invocation row carries —
// printJSON pretty-prints the row's payload verbatim, so a new field on the
// stored payload must show up with no renderer change.
func TestLedgerListRendersCacheSplitAndByteCounts(t *testing.T) {
	tempRepo(t)

	out, err := runRoot(t, "story", "create", "--title", "cost row", "--status", "in_progress")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"from": "in_progress", "to": "done", "agent": "reviewer", "skill": "satelle-story-done-review",
		"tokens_in": 130, "tokens_out": 10, "tokens_total": 140, "usage_available": true,
		"tokens_in_fresh": 100, "tokens_cache_write": 20, "tokens_cache_read": 10,
		"system_prompt_bytes": 4096, "payload_bytes": 512,
	})
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: created.ID, Kind: ledger.KindAgentInvocation, Actor: "reviewer", Body: "invoked", Payload: payload,
	}, time.Now()); err != nil {
		db.Close()
		t.Fatalf("append: %v", err)
	}
	db.Close()

	listOut, err := runRoot(t, "ledger", "list", "--story", created.ID, "--kind", ledger.KindAgentInvocation)
	if err != nil {
		t.Fatalf("ledger list: %v\n%s", err, listOut)
	}
	for _, want := range []string{`"tokens_cache_read": 10`, `"tokens_cache_write": 20`, `"tokens_in_fresh": 100`, `"payload_bytes": 512`, `"system_prompt_bytes": 4096`} {
		if !strings.Contains(listOut, want) {
			t.Errorf("ledger list output missing %q:\n%s", want, listOut)
		}
	}

	// `story cost --by-skill --story <id>` rolls the same row up by skill.
	costOut, err := runRoot(t, "story", "cost", "--by-skill", "--story", created.ID)
	if err != nil {
		t.Fatalf("story cost --by-skill: %v\n%s", err, costOut)
	}
	if !strings.Contains(costOut, "satelle-story-done-review") {
		t.Errorf("story cost --by-skill output missing the skill row:\n%s", costOut)
	}

	// --by-skill requires --all, --story, or a positional id.
	if _, err := runRoot(t, "story", "cost", "--by-skill"); err == nil {
		t.Fatal("story cost --by-skill with no scope should refuse")
	}
	// --all and --story are mutually exclusive.
	if _, err := runRoot(t, "story", "cost", "--by-skill", "--all", "--story", created.ID); err == nil {
		t.Fatal("story cost --by-skill --all --story should refuse (mutually exclusive)")
	}
}
