package verb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// legacyRecords is a story in the subject clone of a bare remote whose
// change_record rows the test writes itself, in the shape a binary before
// sty_659c4133 wrote them: every file in since..head, other stories' included.
type legacyRecords struct {
	t   *testing.T
	r   testutil.TrunkRepos
	id  string
	ctx context.Context
}

func newLegacyRecords(t *testing.T) *legacyRecords {
	t.Helper()
	wireCR(t)
	r := testutil.NewTrunkRepos(t)
	cwd, _ := os.Getwd()
	if err := os.Chdir(r.Subject); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	ctx := context.Background()
	ws, err := requireWorkItem()
	if err != nil {
		t.Fatal(err)
	}
	it, err := ws.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "legacy", Category: "fix"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return &legacyRecords{t: t, r: r, id: it.ID, ctx: ctx}
}

// commit writes content to file in dir and commits it under subject.
func (l *legacyRecords) commit(dir, file, content, subject string) {
	l.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		l.t.Fatal(err)
	}
	l.r.Git(l.t, dir, "add", file)
	l.r.Git(l.t, dir, "commit", "--quiet", "-m", subject)
}

// own commits file in the subject clone under a subject naming the story.
func (l *legacyRecords) own(file, content string) {
	l.commit(l.r.Subject, file, content, "fix: "+file+" ("+l.id+")")
}

// land publishes file to the remote trunk as another story's commit.
func (l *legacyRecords) land(file string) {
	l.t.Helper()
	l.r.Git(l.t, l.r.Pusher, "pull", "--quiet", "--ff-only")
	l.commit(l.r.Pusher, file, file+"\n", "feat: "+file+" (sty_other)")
	l.r.Git(l.t, l.r.Pusher, "push", "--quiet", "origin", "main")
}

func (l *legacyRecords) head() string { return l.r.Head(l.t, l.r.Subject) }

// row appends a change_record row and returns its ledger entry id.
func (l *legacyRecords) row(p changeRecordPayload) string {
	l.t.Helper()
	p.FileCount = len(p.Files)
	raw, _ := json.Marshal(p)
	ls, err := requireLedger()
	if err != nil {
		l.t.Fatal(err)
	}
	e, err := ls.Append(l.ctx, ledger.AppendInput{
		StoryID: l.id, Kind: ledger.KindChangeRecord, Actor: "executor", Body: "legacy", Payload: raw,
	}, time.Now())
	if err != nil {
		l.t.Fatal(err)
	}
	return e.ID
}

// legacyRow appends the pre-sty_659c4133 row for since..head.
func (l *legacyRecords) legacyRow(since, head string, files ...string) string {
	return l.row(changeRecordPayload{From: "in_progress", To: "blocked", SinceSHA: since, HeadSHA: head, Files: files})
}

// recorded is `satelle story diff --recorded`.
func (l *legacyRecords) recorded() StoryDiffResult {
	l.t.Helper()
	req, _ := json.Marshal(map[string]any{"id": l.id, "recorded": true})
	out, err := storyDiff(l.ctx, req)
	if err != nil {
		l.t.Fatal(err)
	}
	var res StoryDiffResult
	if err := json.Unmarshal(out, &res); err != nil {
		l.t.Fatal(err)
	}
	return res
}

func wantRecorded(t *testing.T, res StoryDiffResult, in, notIn []string) {
	t.Helper()
	got := "," + strings.Join(res.Files, ",") + ","
	for _, f := range in {
		if !strings.Contains(got, ","+f+",") {
			t.Errorf("recorded: %s missing from %v", f, res.Files)
		}
	}
	for _, f := range notIn {
		if strings.Contains(got, ","+f+",") {
			t.Errorf("recorded: %s must not be in %v", f, res.Files)
		}
	}
}

// A change_record written before the trunk exclusion lists another story's trunk
// commit that a fast-forward or a trunk merge put in its range. Read, the row
// drops those files and keeps every path the story's own commits, its merge's own
// changes or its uncommitted edits touched (sty_5a6c06d3 AC1).
func TestRecordedChangeSetExcludesForeignTrunkCommitsOnRead(t *testing.T) {
	t.Run("fast-forward", func(t *testing.T) {
		l := newLegacyRecords(t)
		base := l.head()
		l.own("a.go", "a\n")
		l.r.Git(t, l.r.Subject, "push", "--quiet", "origin", "main")
		l.land("Makefile")
		l.land("shared.go")
		l.r.Git(t, l.r.Subject, "pull", "--quiet", "--ff-only")
		l.own("shared.go", "mine\n")
		l.legacyRow(base, l.head(), "Makefile", "a.go", "shared.go")

		res := l.recorded()
		wantRecorded(t, res, []string{"a.go", "shared.go"}, []string{"Makefile"})
		if len(res.UnresolvedRecords) != 0 {
			t.Errorf("unresolved_records=%v, want none", res.UnresolvedRecords)
		}
	})

	t.Run("uncommitted", func(t *testing.T) {
		l := newLegacyRecords(t)
		base := l.head()
		l.land("Makefile")
		l.land("dirty.go")
		l.r.Git(t, l.r.Subject, "pull", "--quiet", "--ff-only")
		l.legacyRow(base, l.head(), "Makefile", "dirty.go")
		if err := os.WriteFile(filepath.Join(l.r.Subject, "dirty.go"), []byte("edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wantRecorded(t, l.recorded(), []string{"dirty.go"}, []string{"Makefile"})
	})

	t.Run("trunk merge", func(t *testing.T) {
		l := newLegacyRecords(t)
		base := l.head()
		l.own("a.go", "a\n")
		l.land("Makefile")
		l.r.Git(t, l.r.Subject, "fetch", "--quiet", "origin")
		l.r.Git(t, l.r.Subject, "merge", "--quiet", "--no-ff", "-m", "Merge main into the branch", "origin/main")
		l.legacyRow(base, l.head(), "Makefile", "a.go")
		wantRecorded(t, l.recorded(), []string{"a.go"}, []string{"Makefile"})
	})

	t.Run("trunk merge that changes a trunk file", func(t *testing.T) {
		l := newLegacyRecords(t)
		base := l.head()
		l.own("a.go", "a\n")
		l.land("Makefile")
		l.land("other.go")
		l.r.Git(t, l.r.Subject, "fetch", "--quiet", "origin")
		l.r.Git(t, l.r.Subject, "merge", "--quiet", "--no-ff", "--no-commit", "origin/main")
		if err := os.WriteFile(filepath.Join(l.r.Subject, "Makefile"), []byte("resolved\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		l.r.Git(t, l.r.Subject, "add", "Makefile")
		l.r.Git(t, l.r.Subject, "commit", "--quiet", "-m", "Merge main into the branch")
		l.legacyRow(base, l.head(), "Makefile", "a.go", "other.go")
		wantRecorded(t, l.recorded(), []string{"a.go", "Makefile"}, []string{"other.go"})
	})

	t.Run("partial row", func(t *testing.T) {
		l := newLegacyRecords(t)
		l.land("Makefile")
		l.r.Git(t, l.r.Subject, "pull", "--quiet", "--ff-only")
		l.row(changeRecordPayload{From: "in_progress", To: "in_progress", PartialEnumeration: true,
			Files: []string{".satelle/principles/gone.md", "Makefile"}})
		res := l.recorded()
		wantRecorded(t, res, []string{".satelle/principles/gone.md", "Makefile"}, nil)
		if len(res.UnresolvedRecords) != 0 || strings.Contains(res.Note, "unresolved") {
			t.Errorf("a partial row claims no range: unresolved_records=%v note=%q", res.UnresolvedRecords, res.Note)
		}
	})

	t.Run("no remote trunk", func(t *testing.T) {
		l := newLegacyRecords(t)
		base := l.head()
		l.land("Makefile")
		l.r.Git(t, l.r.Subject, "pull", "--quiet", "--ff-only")
		l.legacyRow(base, l.head(), "Makefile")
		l.r.Git(t, l.r.Subject, "remote", "remove", "origin")
		res := l.recorded()
		wantRecorded(t, res, []string{"Makefile"}, nil)
		if len(res.UnresolvedRecords) != 0 {
			t.Errorf("unresolved_records=%v, want none", res.UnresolvedRecords)
		}
	})
}

// A row whose range git cannot show keeps its files as recorded and is named in
// unresolved_records and the note, never dropped (sty_5a6c06d3 AC3).
func TestRecordedChangeSetNamesUnresolvedRange(t *testing.T) {
	l := newLegacyRecords(t)
	l.land("Makefile")
	l.r.Git(t, l.r.Subject, "pull", "--quiet", "--ff-only")
	missing := l.legacyRow("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", l.head(), "Makefile")
	noSince := l.legacyRow("", l.head(), "lost.go")

	res := l.recorded()
	wantRecorded(t, res, []string{"Makefile", "lost.go"}, nil)
	if len(res.UnresolvedRecords) != 2 {
		t.Fatalf("unresolved_records=%v, want both rows", res.UnresolvedRecords)
	}
	for _, id := range []string{missing, noSince} {
		if !strings.Contains(strings.Join(res.UnresolvedRecords, "\n"), id) {
			t.Errorf("unresolved_records=%v must name %s", res.UnresolvedRecords, id)
		}
		if !strings.Contains(res.Note, "used as recorded (range unresolved)") || !strings.Contains(res.Note, id) {
			t.Errorf("note=%q must name the unresolved row %s", res.Note, id)
		}
	}
}
