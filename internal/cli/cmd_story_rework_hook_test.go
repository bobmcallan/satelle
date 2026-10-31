package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestReworkSessionID(t *testing.T) {
	engagedA := "a4de0ec7-5809-4d35-8356-9f7c01769098"
	fallbackB := "31261381-b59d-49af-8306-a8a2c925a9fe"
	if got := reworkSessionID(engagedA, fallbackB); got != engagedA {
		t.Fatalf("engaged session = %q, want %q", got, engagedA)
	}
	if got := reworkSessionID("", fallbackB); got != fallbackB {
		t.Fatalf("no engaged session = %q, want fallback %q", got, fallbackB)
	}
	if got := reworkSessionID("  ", fallbackB); got != fallbackB {
		t.Fatalf("blank engaged session = %q, want fallback %q", got, fallbackB)
	}
}

func TestWithRelayMarker(t *testing.T) {
	_ = os.Unsetenv(config.RelayBindingEnv)
	_ = os.Unsetenv(config.RelayItemEnv)
	t.Cleanup(func() {
		_ = os.Unsetenv(config.RelayBindingEnv)
		_ = os.Unsetenv(config.RelayItemEnv)
	})

	err := withRelayMarker("coder", "sty_x", func() error {
		if os.Getenv(config.RelayBindingEnv) != "coder" {
			t.Fatalf("binding env = %q", os.Getenv(config.RelayBindingEnv))
		}
		if os.Getenv(config.RelayItemEnv) != "sty_x" {
			t.Fatalf("item env = %q", os.Getenv(config.RelayItemEnv))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := os.LookupEnv(config.RelayBindingEnv); ok {
		t.Fatal("RelayBindingEnv must be cleared after success")
	}
	if _, ok := os.LookupEnv(config.RelayItemEnv); ok {
		t.Fatal("RelayItemEnv must be cleared after success")
	}

	boom := errors.New("open failed")
	err = withRelayMarker("coder", "sty_x", func() error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if _, ok := os.LookupEnv(config.RelayBindingEnv); ok {
		t.Fatal("RelayBindingEnv must be cleared after error")
	}
	if _, ok := os.LookupEnv(config.RelayItemEnv); ok {
		t.Fatal("RelayItemEnv must be cleared after error")
	}
}

func TestReworkOpenSequenceMarkerOnlyOnCoder(t *testing.T) {
	_ = os.Unsetenv(config.RelayBindingEnv)
	_ = os.Unsetenv(config.RelayItemEnv)
	t.Cleanup(func() {
		_ = os.Unsetenv(config.RelayBindingEnv)
		_ = os.Unsetenv(config.RelayItemEnv)
	})

	type snap struct {
		binding string
		relay   string
		model   string
	}
	var opens []snap
	prev := reworkSessionOpener
	t.Cleanup(func() { reworkSessionOpener = prev })
	reworkSessionOpener = func(
		_ context.Context,
		_ *agentstep.Engine,
		binding string,
		_ agentstep.SessionRole,
		_ workitem.Item,
		_ agentcli.PermissionPolicy,
		_ agentcli.EventHandler,
		modelOverride string,
	) (agentcli.Session, error) {
		opens = append(opens, snap{binding: binding, relay: os.Getenv(config.RelayBindingEnv), model: modelOverride})
		return nopSession{}, nil
	}

	// Mirror the verb's open sequence: marker wraps coder only; --model rides
	// only the coder open (sty_7069bced).
	if err := withRelayMarker("coder", "sty_x", func() error {
		_, err := reworkSessionOpener(context.Background(), nil, "coder", agentstep.SessionRoleDriving, workitem.Item{ID: "sty_x"}, nil, nil, "haiku")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := reworkSessionOpener(context.Background(), nil, "reviewer-consult", agentstep.SessionRoleConsult, workitem.Item{ID: "sty_x"}, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if len(opens) != 2 {
		t.Fatalf("opens = %d, want 2", len(opens))
	}
	if opens[0].binding != "coder" || opens[0].relay != "coder" || opens[0].model != "haiku" {
		t.Fatalf("coder open = %+v, want binding=coder relay=coder model=haiku", opens[0])
	}
	if opens[1].binding != "reviewer-consult" || opens[1].relay != "" || opens[1].model != "" {
		t.Fatalf("consultant open = %+v, want empty relay marker and empty model", opens[1])
	}
}

// nopSession is a closed stub so OpenSessionAs replacements need no transport.
type nopSession struct{}

func (nopSession) Send(context.Context, agentcli.Turn) error { return nil }
func (nopSession) Events() <-chan agentcli.Event {
	ch := make(chan agentcli.Event)
	close(ch)
	return ch
}
func (nopSession) Close() error     { return nil }
func (nopSession) Cancel() error    { return nil }
func (nopSession) Captured() []byte { return nil }

// coderAllocatedRepo builds a repo whose in_progress step allocates agent=coder
// and records the story engaged from the repo's tree by sessionID (sty_7567f047
// AC1 fixture).
func coderAllocatedRepo(t *testing.T, sessionID string) (repo, storyID string) {
	t.Helper()
	repo = tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "planned", "coded", "closed"]
park = { state = "blocked" }
`,
		`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "planner"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "coder"
requires = ["planned"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
	if err := os.MkdirAll(filepath.Join(repo, "internal", "cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "cli", "foo.go"), []byte("package cli\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, now); err != nil {
		t.Fatal(err)
	}
	story, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "rework hook", Body: "goal",
		AcceptanceCriteria: "1. ok", Status: "in_progress", Category: "fix",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	recordEngagementOn(t, db, story.ID, "in_progress", repo, sessionID)
	return repo, story.ID
}

// TestReworkAdoptsTheEngagedSession: the relay adopts the session the story was
// engaged from (its working_on row), so the coder's hook resolves that story as
// its own. The tree decides the engagement; the session only decides Mine.
func TestReworkAdoptsTheEngagedSession(t *testing.T) {
	engagedA := "a4de0ec7-5809-4d35-8356-9f7c01769098"
	fallbackB := "31261381-b59d-49af-8306-a8a2c925a9fe"
	repo, storyID := coderAllocatedRepo(t, engagedA)

	origTree := sessionWorktree
	t.Cleanup(func() { sessionWorktree = origTree })
	sessionWorktree = func() string { return repo }

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := db.Ledger.ListNewest(context.Background(), ledger.ListFilter{StoryID: storyID, Kind: ledger.KindWorkingOn, Limit: 50})
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	w, ok := verb.LatestWorkingOn(rows, "")
	if !ok {
		t.Fatal("no working_on row recorded")
	}
	sid := reworkSessionID(w.Session, fallbackB)
	if sid != engagedA {
		t.Fatalf("reworkSessionID = %q, want engaged %q", sid, engagedA)
	}

	t.Setenv(config.SessionEnv, sid)
	info, engaged, _, err := resolveEngagements(bindSessionID(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !engaged || info.ItemID != storyID || !info.Mine {
		t.Fatalf("hook with adopted session: engaged=%v item=%q mine=%v", engaged, info.ItemID, info.Mine)
	}

	t.Setenv(config.SessionEnv, fallbackB)
	info, engaged, _, err = resolveEngagements(bindSessionID(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !engaged || info.ItemID != storyID || info.Mine {
		t.Fatalf("another session in the same tree resolves the tree's story, not as its own: engaged=%v item=%q mine=%v", engaged, info.ItemID, info.Mine)
	}
}
