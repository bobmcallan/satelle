package cli

// sty_ce858cb3 — the edit gate records each substrate-lane write it allows, so
// another story's change set can leave out a file that story did not write.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/spf13/cobra"
)

// AC6: an allowed write appends one substrate_written row naming the story and
// path, a refused one appends none, and a write whose row cannot be appended is
// refused.
func TestGateRecordsSubstrateWrite(t *testing.T) {
	t.Run("allowed", func(t *testing.T) {
		e := newLaneEnv(t, []laneSpec{
			{key: "S", category: "substrate", session: "sess"},
			{key: "F", category: "fix", session: "other"},
		})
		target := filepath.Join(e.repo, laneRel)
		if err, out := e.gate(t, "edit", target, e.seats("sess", target)); err != nil {
			t.Fatalf("the substrate seat's write must pass: %v\n%s", err, out)
		}
		n, rows := rowsByStory(t, ledger.KindSubstrateWritten)
		if len(rows) != 1 || n[e.ids["S"]] != 1 {
			t.Fatalf("want one substrate_written row on S, got %v", n)
		}
		var p verb.SubstrateWrittenPayload
		if err := json.Unmarshal(rows[0].Payload, &p); err != nil || p.Path != laneRel {
			t.Errorf("row payload = %s (%v), want path %s", rows[0].Payload, err, laneRel)
		}
	})

	t.Run("refused", func(t *testing.T) {
		e := newLaneEnv(t, []laneSpec{
			{key: "S", category: "substrate", session: "sess"},
			{key: "F", category: "fix", session: "sess"},
		})
		target := filepath.Join(e.trees["F"], laneRel)
		if err, out := e.gate(t, "edit", target, e.seats("sess", target)); err == nil {
			t.Fatalf("an edit attributed to F must be refused:\n%s", out)
		}
		if rows := ledgerRows(t, ledger.KindSubstrateWritten); len(rows) != 0 {
			t.Errorf("a refused write records nothing, got %d row(s)", len(rows))
		}
	})

	t.Run("append fails", func(t *testing.T) {
		e := newLaneEnv(t, []laneSpec{
			{key: "S", category: "substrate", session: "sess"},
			{key: "F", category: "fix", session: "other"},
		})
		orig := recordSubstrateWritesFn
		t.Cleanup(func() { recordSubstrateWritesFn = orig })
		recordSubstrateWritesFn = func([]pendingWrite) error { return errors.New("ledger unavailable") }
		target := filepath.Join(e.repo, laneRel)
		for _, via := range []string{"edit", "bash"} {
			if err, out := e.gate(t, via, target, e.seats("sess", target)); err == nil {
				t.Errorf("%s: a write that cannot be recorded must be refused:\n%s", via, out)
			}
		}
		if n, _ := rowsByStory(t, ledger.KindSubstrateChangeNote); len(n) != 0 {
			t.Errorf("a refused write notes nobody: %v", n)
		}
	})
}

// AC6: under the lock opt-out the gate judges no substrate path, so another
// story's write is recorded by no row and stays in this story's change set.
func TestLockOptOutForeignWriteListed(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv("SATELLE_SERVER_ENDPOINT", "none")
	repo := t.TempDir()
	gitTree(t, repo, ".satelle/")
	const rel = ".satelle/skills/b.md"
	writeRepoFile(t, repo, ".satelle/satelle.toml", lockExemptToml+"lock_substrate_paths = []\n")
	t.Setenv("SATELLE_CONFIG", filepath.Join(repo, ".satelle", "satelle.toml"))
	editBeforeEngage(t, repo, rel, "# b\n")
	a := engageSyncStory(t, repo)
	a.engage()

	target := filepath.Join(repo, rel)
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := substrateLockGate(cmd, []byte(claudeEditEvent(target)), target, &lockSeats{}); err != nil {
		t.Fatalf("the opt-out locks nothing: %v\n%s", err, out.String())
	}
	editAfterEngage(t, repo, rel, "# b by another story\n")

	if rows, err := a.db.Ledger.List(context.Background(), ledger.ListFilter{Kind: ledger.KindSubstrateWritten}); err != nil || len(rows) != 0 {
		t.Fatalf("the opt-out records no substrate_written row, got %d (%v)", len(rows), err)
	}
	if rows := ledgerRows(t, ledger.KindSubstrateWritten); len(rows) != 0 {
		t.Fatalf("the opt-out records no substrate_written row in the repo ledger, got %d", len(rows))
	}
	res, err := verb.StoryDiff(context.Background(), a.id, verb.StoryDiffOpts{IncludeSubstrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Files, rel) {
		t.Errorf("an unattributed write stays in A's change set, got %v", res.Files)
	}
}
