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
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestReworkSessionID(t *testing.T) {
	leaseA := "a4de0ec7-5809-4d35-8356-9f7c01769098"
	fallbackB := "31261381-b59d-49af-8306-a8a2c925a9fe"
	if got := reworkSessionID(lease.Lease{SessionID: leaseA}, true, fallbackB); got != leaseA {
		t.Fatalf("stamped lease = %q, want %q", got, leaseA)
	}
	if got := reworkSessionID(lease.Lease{}, true, fallbackB); got != fallbackB {
		t.Fatalf("unstamped lease = %q, want fallback %q", got, fallbackB)
	}
	if got := reworkSessionID(lease.Lease{SessionID: leaseA}, false, fallbackB); got != fallbackB {
		t.Fatalf("absent lease = %q, want fallback %q", got, fallbackB)
	}
}

func TestPickSessionSeatReworkIdentity(t *testing.T) {
	leaseA := "a4de0ec7-5809-4d35-8356-9f7c01769098"
	fallbackB := "31261381-b59d-49af-8306-a8a2c925a9fe"
	live := []seatInfo{{
		ItemID: "sty_9134015e", SessionID: leaseA, Engaged: true,
		State: "in_progress", StoryStatus: "in_progress", StateAgent: "coder",
	}}
	origTree := sessionWorktree
	t.Cleanup(func() { sessionWorktree = origTree })
	sessionWorktree = func() string { return "" }

	if pick, mine := pickSessionSeat(live, fallbackB); pick.ItemID != "" || mine {
		t.Fatalf("23:04 failure: session B must drop lease stamped A; got item=%q mine=%v", pick.ItemID, mine)
	}
	adopted := reworkSessionID(lease.Lease{SessionID: leaseA}, true, fallbackB)
	pick, mine := pickSessionSeat(live, adopted)
	if pick.ItemID != "sty_9134015e" || !mine {
		t.Fatalf("reworkSessionID must select the stamped lease; got item=%q mine=%v", pick.ItemID, mine)
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
// and acquires a live seat stamped with sessionID (sty_7567f047 AC1 fixture).
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
	if _, _, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: story.ID, Kind: "story", Owner: lease.ResolveOwner(),
		State: "in_progress", StorySeat: true, Worktree: repo, SessionID: sessionID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Leases.Confirm(ctx, story.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	return repo, story.ID
}

func TestReworkSeatResolvesStampedLease(t *testing.T) {
	leaseA := "a4de0ec7-5809-4d35-8356-9f7c01769098"
	fallbackB := "31261381-b59d-49af-8306-a8a2c925a9fe"
	repo, storyID := coderAllocatedRepo(t, leaseA)

	origTree := sessionWorktree
	t.Cleanup(func() { sessionWorktree = origTree })
	sessionWorktree = func() string { return repo }

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	l, err := db.Leases.Get(context.Background(), storyID)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	sid := reworkSessionID(l, true, fallbackB)
	if sid != leaseA {
		t.Fatalf("reworkSessionID = %q, want lease %q", sid, leaseA)
	}

	t.Setenv(config.SessionEnv, sid)
	info, engaged, err := resolveSeat(false, bindSessionID(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !engaged || info.ItemID != storyID || !info.Mine {
		t.Fatalf("hook with adopted session: engaged=%v item=%q mine=%v", engaged, info.ItemID, info.Mine)
	}
	// Verb seat closure uses the same id.
	info2, engaged2, err := resolveSeat(true, sid)
	if err != nil || !engaged2 || info2.ItemID != storyID {
		t.Fatalf("verb seat with adopted session: engaged=%v item=%q err=%v", engaged2, info2.ItemID, err)
	}

	t.Setenv(config.SessionEnv, fallbackB)
	_, engagedB, err := resolveSeat(false, bindSessionID(nil))
	if err != nil {
		t.Fatal(err)
	}
	if engagedB {
		t.Fatal("old behaviour (session B): must not engage a lease stamped with session A")
	}
}
