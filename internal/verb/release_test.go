package verb_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// releaseEnv is a git repo with the change route wired and an ungated gater, so
// a story can be driven to done the way a lane drives it.
func releaseEnv(t *testing.T, workflows map[string]string) *store.DB {
	t.Helper()
	withWiring(t)
	dir := gitRepo(t)
	chdir(t, dir)
	db := wireWithWorkflowsStore(t, workflows)
	verb.SetStoryDir(filepath.Join(t.TempDir(), "stories"))
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	return db
}

func routeStory(t *testing.T, title string, tags ...string) workitem.Item {
	t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": title, "body": "goal", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": append([]string{"workflow:cr-wf"}, tags...),
	}), &it); err != nil || it.ID == "" {
		t.Fatalf("story-create: %v %+v", err, it)
	}
	return it
}

// doneStory drives a fresh story backlog → in_progress → done.
func doneStory(t *testing.T, title string) workitem.Item {
	t.Helper()
	it := routeStory(t, title)
	for _, s := range []string{"in_progress", "done"} {
		if _, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": s}); err != nil {
			t.Fatalf("story-set %s: %v", s, err)
		}
	}
	return storyByID(t, it.ID)
}

func storyByID(t *testing.T, id string) workitem.Item {
	t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-get", map[string]any{"id": id}), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func releaseRows(t *testing.T, db *store.DB, id string) []ledger.Entry {
	t.Helper()
	rows, err := db.Ledger.ListByStory(context.Background(), id, ledger.KindReleaseRecord)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// AC1: one release is recorded against a done story and an open one; each gets
// one row carrying the commit and release, and neither status moves.
func TestReleaseRecordAgainstDoneAndOpenStories(t *testing.T) {
	db := releaseEnv(t, changeWF)
	verb.SetReleaseFields([]string{"channel"})
	done := doneStory(t, "shipped")
	open := routeStory(t, "still open")
	if done.Status != "done" || open.Status != "backlog" {
		t.Fatalf("fixture statuses = %s / %s", done.Status, open.Status)
	}

	call(t, "story-release-record", map[string]any{
		"ids": []string{done.ID, open.ID}, "commit": "abc1234", "release": "v1.2.3",
		"fields": map[string]string{"channel": "stable"},
	})

	for _, it := range []workitem.Item{done, open} {
		rows := releaseRows(t, db, it.ID)
		if len(rows) != 1 {
			t.Fatalf("%s: release rows = %d, want 1", it.ID, len(rows))
		}
		if rows[0].Body != "release v1.2.3 @ abc1234" {
			t.Errorf("%s: body = %q", it.ID, rows[0].Body)
		}
		var p struct {
			Commit, Release string
			Fields          map[string]string
		}
		if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Commit != "abc1234" || p.Release != "v1.2.3" || p.Fields["channel"] != "stable" {
			t.Errorf("%s: payload = %+v", it.ID, p)
		}
		if got := storyByID(t, it.ID).Status; got != it.Status {
			t.Errorf("%s: status %s → %s; a release record must not move status", it.ID, it.Status, got)
		}
	}
}

// AC1: an unknown id refuses the whole record, named, and writes no row.
func TestReleaseRecordUnknownIDWritesNothing(t *testing.T) {
	db := releaseEnv(t, changeWF)
	ok := routeStory(t, "real")
	_, err := dispatchRaw(t, "story-release-record", map[string]any{
		"ids": []string{ok.ID, "sty_nope"}, "commit": "abc", "release": "v1",
	})
	mustContain(t, err, "sty_nope", "nothing was recorded")
	if rows := releaseRows(t, db, ok.ID); len(rows) != 0 {
		t.Errorf("a refused record wrote %d row(s) on %s", len(rows), ok.ID)
	}
}

// AC1: with [release] fields declared, an undeclared key refuses, named.
func TestReleaseRecordUndeclaredFieldRefused(t *testing.T) {
	db := releaseEnv(t, changeWF)
	verb.SetReleaseFields([]string{"channel"})
	it := routeStory(t, "field")
	_, err := dispatchRaw(t, "story-release-record", map[string]any{
		"ids": []string{it.ID}, "commit": "abc", "release": "v1",
		"fields": map[string]string{"region": "eu"},
	})
	mustContain(t, err, `"region"`, "[release] fields")
	if rows := releaseRows(t, db, it.ID); len(rows) != 0 {
		t.Errorf("a refused record wrote %d row(s)", len(rows))
	}
	_, err = dispatchRaw(t, "story-release-record", map[string]any{"ids": []string{it.ID}, "release": "v1"})
	mustContain(t, err, "commit and release")
}

// AC1: a session that is not the story's own — another owner, while the story
// performs — still records the release, and the story's presence is left alone.
func TestReleaseRecordFromAnotherSession(t *testing.T) {
	db := releaseEnv(t, changeWF)
	t.Setenv("SATELLE_OWNER", "owner-a")
	t.Setenv(config.SessionEnv, "sess-a")
	it := routeStory(t, "engaged elsewhere")
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}); err != nil {
		t.Fatalf("engage: %v", err)
	}
	if before, ok := presenceRow(t, it.ID); !ok || before.Session != "sess-a" {
		t.Fatalf("working_on = %+v, %v", before, ok)
	}

	t.Setenv("SATELLE_OWNER", "release-task")
	t.Setenv(config.SessionEnv, "sess-release")
	call(t, "story-release-record", map[string]any{"ids": []string{it.ID}, "commit": "abc", "release": "v1"})

	rows := releaseRows(t, db, it.ID)
	if len(rows) != 1 || !strings.Contains(string(rows[0].Payload), `"owner":"release-task"`) {
		t.Fatalf("release rows = %+v, want one recorded by release-task", rows)
	}
	if after, ok := presenceRow(t, it.ID); !ok || after.Session != "sess-a" {
		t.Errorf("recording a release must not move the story's presence: %+v, %v", after, ok)
	}
	if got := storyByID(t, it.ID).Status; got != "in_progress" {
		t.Errorf("status = %s, want in_progress", got)
	}
}

// recoverFromDoneWF declares a recover edge out of the terminal state.
var recoverFromDoneWF = routeHalves(
	`["*"]
obligations = ["raised", "coded", "closed"]
recover = { step = "in_progress", from = ["done"] }
`, changeWF["step"][strings.Index(changeWF["step"], "[raised]"):])

// AC3: a done story is never reopened by story-set — refused with its id named,
// status unchanged — with or without a declared edge back out of done.
func TestStorySetRefusesReopeningADoneStory(t *testing.T) {
	spec, err := wfdot.ParseRoute(recoverFromDoneWF["done"], recoverFromDoneWF["step"], "feature", nil)
	if err != nil || !spec.HasEdge("done", "in_progress") {
		t.Fatalf("control: the fixture route must declare done → in_progress (err %v)", err)
	}
	for name, wf := range map[string]map[string]string{"no edge": changeWF, "declared edge": recoverFromDoneWF} {
		t.Run(name, func(t *testing.T) {
			releaseEnv(t, wf)
			it := doneStory(t, "closed history")
			if it.Status != "done" {
				t.Fatalf("fixture status = %s", it.Status)
			}
			_, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
			mustContain(t, err, "refusing to reopen "+it.ID, "follows:"+it.ID)
			if got := storyByID(t, it.ID).Status; got != "done" {
				t.Errorf("status = %s after a refused reopen, want done", got)
			}
		})
	}
}

// AC4: a follow-up story carries a back-reference tag to a done story; an unknown
// target is refused; with no namespace configured nothing is checked.
func TestBackReferenceTagOnFollowUpStory(t *testing.T) {
	backRefs := func() {
		cfg := surfaceVocab()
		cfg.Tags.BackReferenceNamespaces = []string{"follows", "regression-of"}
		verb.SetTagVocabulary(cfg)
	}

	t.Run("follows a done story", func(t *testing.T) {
		releaseEnv(t, changeWF)
		backRefs()
		done := doneStory(t, "shipped")
		follow := routeStory(t, "follow-up", "follows:"+done.ID)
		if got := storyByID(t, follow.ID); !hasTag(got.Tags, "follows:"+done.ID) {
			t.Errorf("follow-up tags = %v, want follows:%s", got.Tags, done.ID)
		}
		if got := storyByID(t, done.ID); got.Status != "done" || got.UpdatedAt != done.UpdatedAt {
			t.Errorf("the followed story changed: %s @ %v → %s @ %v", done.Status, done.UpdatedAt, got.Status, got.UpdatedAt)
		}
	})

	t.Run("unknown target refused at create and set", func(t *testing.T) {
		releaseEnv(t, changeWF)
		backRefs()
		_, err := dispatchRaw(t, "story-create", map[string]any{
			"title": "bad ref", "body": "goal", "acceptance_criteria": "1. ok", "category": "feature",
			"tags": []string{"regression-of:sty_nope"},
		})
		mustContain(t, err, "regression-of:sty_nope", "does not name a story")
		it := routeStory(t, "later ref")
		mustContain(t, addTags(t, it.ID, "regression-of:sty_nope"), "regression-of:sty_nope")
	})

	t.Run("no namespace configured checks nothing", func(t *testing.T) {
		releaseEnv(t, changeWF)
		verb.SetTagVocabulary(surfaceVocab())
		if _, err := dispatchRaw(t, "story-create", map[string]any{
			"title": "free tag", "body": "goal", "acceptance_criteria": "1. ok", "category": "feature",
			"tags": []string{"follows:sty_nope"},
		}); err != nil {
			t.Fatalf("with no back-reference namespace a follows: tag is free-form: %v", err)
		}
	})
}
