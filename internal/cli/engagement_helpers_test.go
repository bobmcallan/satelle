package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// runRootIn runs the command tree with stdin bound to the given text (hook
// handlers read the event from stdin). Flag state is reset first: the tree is
// registered once, so flag variables (and their Changed bits) persist across
// runs in the same process.
func runRootIn(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	resetFlagState(root)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := executeRoot(root)
	return buf.String(), err
}

func editEvent(path string) string {
	return fmt.Sprintf(`{"tool_input":{"file_path":%q}}`, path)
}

func bashEvent(cmd string) string {
	return fmt.Sprintf(`{"tool_input":{"command":%q}}`, cmd)
}

// denyReasonContains reports whether the JSON deny payload's reason contains
// the canonical text (accounting for JSON escaping of quotes and < >).
func denyReasonContains(payload, reason string) bool {
	if strings.Contains(payload, reason) {
		return true
	}
	esc := strings.ReplaceAll(reason, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	esc = strings.ReplaceAll(esc, `<`, `\u003c`)
	esc = strings.ReplaceAll(esc, `>`, `\u003e`)
	if strings.Contains(payload, esc) {
		return true
	}
	// Distinctive stem without punctuation that JSON rewrites.
	stem := reason
	if i := strings.Index(stem, " — "); i > 0 {
		stem = stem[:i]
	}
	if i := strings.Index(stem, ". "); i > 40 {
		stem = stem[:i]
	}
	return strings.Contains(payload, stem) || strings.Contains(payload, strings.ReplaceAll(stem, `"`, `\"`))
}

func stubWorktree(t *testing.T, tree string) {
	t.Helper()
	orig := sessionWorktree
	t.Cleanup(func() { sessionWorktree = orig })
	sessionWorktree = func() string { return tree }
}

func dirtyTree(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "internal", "foo.go"), []byte("package internal\n\nvar edited = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recordEngagementOn appends the working_on row a story set writes when id is
// engaged at state from tree by session, through db.
func recordEngagementOn(t *testing.T, db *store.DB, id, state, tree, session string) {
	t.Helper()
	p := verb.WorkingOnPayload{Session: session, Worktree: tree, State: state}
	payload, _ := json.Marshal(p)
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: id, Kind: ledger.KindWorkingOn, Actor: "executor",
		Body: verb.WorkingOnText(p), Payload: payload,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("record working_on: %v", err)
	}
}

// recordEngagement is recordEngagementOn through the repo's runtime store.
func recordEngagement(t *testing.T, id, state, tree, session string) {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	recordEngagementOn(t, db, id, state, tree, session)
}

// engageHere records id as engaged at state from the working directory's tree
// by the resolving session.
func engageHere(t *testing.T, id, state string) {
	t.Helper()
	recordEngagement(t, id, state, sessionWorktreeUncached(), config.ResolveSession())
}

// engageElsewhere records id as engaged at state from a tree that is not this
// one, so the story performs but nothing is engaged from here.
func engageElsewhere(t *testing.T, id, state string) {
	t.Helper()
	recordEngagement(t, id, state, filepath.Join(t.TempDir(), "elsewhere"), "session-elsewhere")
}

// engagedRepo scaffolds a repo with a wildcard workflow and an in_progress story
// engaged from the repo's tree by the resolving session.
func engagedRepo(t *testing.T) (repo, storyID string) {
	t.Helper()
	repo = tempRepo(t)
	// Ensure cwd is the repo so foreign-tree fence and relative paths work.
	t.Chdir(repo)

	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRoute(t, wfDir, wildcardRouteDone, wildcardRouteStep)
	// Non-exempt target for gate tests.
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "foo.go"), []byte("package internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
		t.Fatalf("doc sync: %v", err)
	}
	sty, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "engaged story", Body: "goal",
		AcceptanceCriteria: "1. ok", Status: "in_progress", Category: "chore",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("create story: %v", err)
	}
	storyID = sty.ID
	recordEngagementOn(t, db, storyID, "in_progress", sessionWorktreeUncached(), config.ResolveSession())
	return repo, storyID
}

const wildcardRouteDone = `["*"]
obligations = ["raised", "coded", "closed"]
`

const wildcardRouteStep = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`

// engagementFixture is where the fixture records the story engaged from before
// a hook fires.
type engagementFixture string

const (
	engagedNowhere   engagementFixture = "nowhere"   // performing story, no working_on row
	engagedHere      engagementFixture = "here"      // engaged from this tree by THIS session
	engagedBySibling engagementFixture = "sibling"   // engaged from this tree by a sibling session
	engagedElsewhere engagementFixture = "elsewhere" // engaged from another tree
)

const (
	thisSession    = "session-B"
	siblingSession = "session-A"
)

// stopcheckRepo scaffolds a git-tracked repo with a wildcard route and an
// in_progress story recorded engaged as fix says, then points the hooks'
// session identity at thisSession.
func stopcheckRepo(t *testing.T, fix engagementFixture) (repo, storyID string) {
	t.Helper()
	repo = tempRepo(t)
	t.Chdir(repo)
	t.Setenv(config.SessionEnv, thisSession)
	// The exempt set is configuration; declare it so the exempt-only case
	// exercises the same predicate a real repo does.
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("[review]\ngate_create = false\n\n[gate]\nedit_exempt_paths = [\".satelle/\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir, wildcardRouteDone, wildcardRouteStep)
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "foo.go"), []byte("package internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// dirtyGatedPaths asks git; the tree must be a repo with a clean baseline.
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "baseline"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
		t.Fatalf("doc sync: %v", err)
	}
	sty, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "stopcheck engagement scope", Body: "goal",
		AcceptanceCriteria: "1. ok", Status: "in_progress", Category: "chore",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("create story: %v", err)
	}
	storyID = sty.ID
	tree := sessionWorktreeUncached()
	switch fix {
	case engagedHere:
		recordEngagementOn(t, db, storyID, "in_progress", tree, thisSession)
	case engagedBySibling:
		recordEngagementOn(t, db, storyID, "in_progress", tree, siblingSession)
	case engagedElsewhere:
		recordEngagementOn(t, db, storyID, "in_progress", filepath.Join(t.TempDir(), "elsewhere"), siblingSession)
	}
	return repo, storyID
}

// startTransitionOn appends an open transition_started row on id toward to,
// held by this live process, through db, and returns the row's id.
func startTransitionOn(t *testing.T, db *store.DB, id, to string) string {
	t.Helper()
	payload, _ := json.Marshal(ledger.TransitionStartedPayload{
		To: to, Pid: os.Getpid(), Host: ledger.Hostname(), Session: config.ResolveSession(),
	})
	e, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: id, Kind: ledger.KindTransitionStarted, Actor: "executor",
		Body: "transition started", Payload: payload,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("record transition_started: %v", err)
	}
	return e.ID
}

// onlyStoryID returns the id of the single story in the repo's runtime store.
func onlyStoryID(t *testing.T) string {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	items, err := db.Stories.List(context.Background(), workitem.ListFilter{Kind: workitem.KindStory})
	if err != nil || len(items) != 1 {
		t.Fatalf("stories: %v n=%d", err, len(items))
	}
	return items[0].ID
}

// attributionWFs is a route where the epic-level `ready` step is a performing
// step that does NOT wait on children, and `in_progress` is a dispatched coder
// step — the shape sty_fbbb4aee was observed on.
func attributionWFs() []docindex.Doc {
	return routeWFs(
		`["*"]
obligations = ["raised", "ready", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[ready]
status = "ready"
agent = "ready-reviewer"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "coder"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
}

// attributionRepo writes attributionWFs into a fresh repo, chdirs into it and
// returns the open store plus a helper that files a story at a status.
func attributionRepo(t *testing.T) (*store.DB, func(title, status string) workitem.Item) {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range attributionWFs() {
		if err := os.WriteFile(filepath.Join(wfDir, d.Name+".toml"), []byte(d.Body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, now); err != nil {
		t.Fatal(err)
	}
	create := func(title, status string) workitem.Item {
		it, err := db.Stories.Create(ctx, workitem.CreateInput{
			Kind: workitem.KindStory, Title: title, Body: "goal", AcceptanceCriteria: "1. ok",
			Status: status, Category: "feature",
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	return db, create
}
