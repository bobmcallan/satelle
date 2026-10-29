package verb_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// A substrate file another story wrote through the edit gate, from the bytes this
// story engaged on, is not this story's change (sty_ce858cb3).

// seed puts a skill on disk before any engagement, so only a later write can put
// it in the window.
func (e *substrateEnv) seed(name, body string) string {
	e.t.Helper()
	path := filepath.Join(e.skillDir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour) // time-subject: mtime must precede the engagement baseline
	if err := os.Chtimes(path, at, at); err != nil {
		e.t.Fatal(err)
	}
	return ".satelle/skills/" + name
}

// gate records storyID's gated write to rel, as the edit gate does before the
// write runs.
func (e *substrateEnv) gate(storyID, rel string) {
	e.t.Helper()
	if err := verb.RecordSubstrateWritten(context.Background(), e.db.Ledger, storyID, e.dir, rel); err != nil {
		e.t.Fatal(err)
	}
}

func (e *substrateEnv) lastChangeRecordFiles(id string) []string {
	e.t.Helper()
	rows, err := e.db.Ledger.ListByStory(context.Background(), id, ledger.KindChangeRecord)
	if err != nil || len(rows) == 0 {
		e.t.Fatalf("no change_record for %s: %v", id, err)
	}
	var p struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(rows[len(rows)-1].Payload, &p); err != nil {
		e.t.Fatal(err)
	}
	return p.Files
}

// AC6: of four files changed during A's engagement only the one B alone wrote
// through the gate, from the bytes A engaged on, leaves A's diff and change record.
func TestForeignStoryWriteExcluded(t *testing.T) {
	e := newSubstrateEnv(t)
	w, x, y, z := e.seed("w.md", "# w\n"), e.seed("x.md", "# x\n"), e.seed("y.md", "# y\n"), e.seed("z.md", "# z\n")
	a, b := e.story("A"), e.story("B")
	e.set(a.ID, "in_progress")

	e.gate(b.ID, w)
	e.write("w.md", "# w by B\n")
	e.gate(a.ID, x)
	e.write("x.md", "# x by A\n")
	e.gate(b.ID, x)
	e.write("x.md", "# x by B\n")
	e.write("y.md", "# y unrecorded\n")
	e.gate(b.ID, y)
	e.write("y.md", "# y by B\n")
	e.write("z.md", "# z unrecorded\n")

	res := e.diff(a.ID, verb.StoryDiffOpts{Patch: true, IncludeSubstrate: true})
	if containsPath(res.Files, w) || strings.Contains(res.Stat, w) || strings.Contains(res.Patch, "w by B") {
		t.Errorf("B's write from A's engaged bytes must leave A's diff: files %v\nstat:\n%s", res.Files, res.Stat)
	}
	for _, kept := range []string{x, y, z} {
		if !containsPath(res.Files, kept) {
			t.Errorf("%s must stay in A's diff, got %v", kept, res.Files)
		}
	}

	e.set(a.ID, "done")
	files := e.lastChangeRecordFiles(a.ID)
	if containsPath(files, w) {
		t.Errorf("A's change record must omit %s, got %v", w, files)
	}
	for _, kept := range []string{x, y, z} {
		if !containsPath(files, kept) {
			t.Errorf("%s must stay in A's change record, got %v", kept, files)
		}
	}
}

// AC6: a story with no substrate_baseline (engaged before the row existed) has
// nothing to measure another story's write against, so the file stays listed.
func TestForeignWriteNoBaselineKept(t *testing.T) {
	e := newSubstrateEnv(t)
	w := e.seed("w.md", "# w\n")
	a, b := e.story("A"), e.story("B")
	head, err := exec.Command("git", "-C", e.dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"head_sha": strings.TrimSpace(string(head)), "to": "in_progress"})
	if _, err := e.db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: a.ID, Kind: ledger.KindEngagementBaseline, Actor: "executor", Payload: payload,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	e.gate(b.ID, w)
	e.write("w.md", "# w by B\n")
	if res := e.diff(a.ID, verb.StoryDiffOpts{IncludeSubstrate: true}); !containsPath(res.Files, w) {
		t.Errorf("with no baseline the file must stay listed, got %v", res.Files)
	}
}

// AC6: engaging a story records one substrate_baseline holding the hash of every
// substrate file on disk.
func TestEngagementRecordsBaseline(t *testing.T) {
	e := newSubstrateEnv(t)
	w := e.seed("w.md", "# w\n")
	a := e.story("A")
	e.set(a.ID, "in_progress")
	rows, err := e.db.Ledger.ListByStory(context.Background(), a.ID, ledger.KindSubstrateBaseline)
	if err != nil || len(rows) != 1 {
		t.Fatalf("want one substrate_baseline, got %d (%v)", len(rows), err)
	}
	var p verb.SubstrateBaselinePayload
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("# w\n"))
	if got := p.Files[w]; got != hex.EncodeToString(sum[:]) || len(p.Files) != 1 {
		t.Errorf("baseline must hash the file on disk: %+v", p)
	}
}
