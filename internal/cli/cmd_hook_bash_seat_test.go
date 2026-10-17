package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// seatHeldBySessionA leaves the repo's story seat live and held by sess-A, so a
// Bash event from sess-B is attributed to a foreign seat holder.
func seatHeldBySessionA(t *testing.T) {
	t.Helper()
	repo, storyID := liveSeatRepo(t)
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	_ = db.Leases.ForceRelease(ctx, storyID)
	if _, _, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: storyID, Kind: "story", Owner: lease.ResolveOwner(),
		State: "in_progress", StorySeat: true, SessionID: "sess-A",
		Worktree: repo,
	}); err != nil {
		t.Fatal(err)
	}
}

func sessionBBashEvent(cmd string) string {
	return fmt.Sprintf(`{"session_id":"sess-B","tool_input":{"command":%q}}`, cmd)
}

// TestBashGateForeignSeat (sty_bc78617c AC2/AC3): with a live seat held by a
// different session, a read-only shell command and writes confined to temp
// paths pass both PreToolUse handlers, while real tree mutations are still
// refused as not attributed to the seat holder.
func TestBashGateForeignSeat(t *testing.T) {
	seatHeldBySessionA(t)
	scratch := filepath.Join(t.TempDir(), "scratchpad")
	t.Setenv("SCRATCH", scratch)
	t.Setenv("TMPDIR", t.TempDir())

	allowed := []string{
		"sed -n 332,463p internal/foo.go",
		"mkdir -p " + scratch + "/octop; curl -sfL https://x/y -o " + scratch + "/octop/f",
		"mkdir -p /tmp/sty_bc78617c_octop; curl -sfL https://x/y -o /tmp/sty_bc78617c_octop/f",
		"mkdir -p $SCRATCH/octop",
		"mkdir -p ${TMPDIR}/octop && touch $TMPDIR/octop/f",
		"echo x > $SCRATCH/out",
	}
	for _, c := range allowed {
		for _, sub := range []string{"gate", "commitgate"} {
			if out, err := runRootIn(t, sessionBBashEvent(c), "hook", sub); err != nil {
				t.Errorf("hook %s denied %q under a foreign seat: %v\n%s", sub, c, err, out)
			}
		}
	}

	denied := []string{
		"sed -i s/a/b/ internal/foo.go",
		"sed -i 's/a/b/' internal/foo.go",
		`sed -i "s/a/b/" internal/foo.go`,
		"sed -i -e 's/a/b/' internal/foo.go",
		"rm internal/x.go",
		"echo x > internal/x.go",
		"mkdir -p internal/newdir",
		"mkdir -p $NO_SUCH_VAR_FOR_TEST/octop",
	}
	for _, c := range denied {
		for _, sub := range []string{"gate", "commitgate"} {
			out, err := runRootIn(t, sessionBBashEvent(c), "hook", sub)
			if err == nil {
				t.Errorf("hook %s allowed %q under a foreign seat:\n%s", sub, c, out)
				continue
			}
			if !strings.Contains(out, "holds the live engagement seat") {
				t.Errorf("hook %s deny of %q is not the seat-holder reason:\n%s", sub, c, out)
			}
		}
	}
}

// performingStorySeatless adds a second performing story with no seat, so the
// repo has two performing stories and a session that holds neither.
func performingStorySeatless(t *testing.T) {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Stories.Create(context.Background(), workitem.CreateInput{
		Kind: workitem.KindStory, Title: "second performing", Body: "goal",
		AcceptanceCriteria: "1. ok", Status: "in_progress", Category: "chore",
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

// bashSeatState is one performing-story/seat setup and the deny text a repo-tree
// write gets under it.
type bashSeatState struct {
	name     string
	setup    func(t *testing.T)
	wantDeny string
}

// bashSeatStateSetups are the performing-story/seat counts a Bash write is judged
// under, each producing its own deny text.
func bashSeatStateSetups() []bashSeatState {
	return []bashSeatState{
		{
			name:     "no performing story, no seat",
			setup:    func(t *testing.T) { t.Chdir(tempRepo(t)) },
			wantDeny: noEngagedStoryEditReason,
		},
		{
			name:     "one performing story, live seat held by another session",
			setup:    seatHeldBySessionA,
			wantDeny: "holds the live engagement seat",
		},
		{
			name: "two performing stories, none holds a seat for this session",
			setup: func(t *testing.T) {
				seatHeldBySessionA(t)
				performingStorySeatless(t)
			},
			wantDeny: "more than one performing story, none holds a seat for session sess-B",
		},
	}
}

// TestBashScratchRedirectAnySeatState (sty_e642df17): a shell command whose only
// writes are redirects into an exempt location passes the PreToolUse hook with no
// story engaged by this session, whatever the seat state — quoted or not, with a
// descriptor duplication after it — while a redirect into the repo tree is still
// refused with that state's reason.
func TestBashScratchRedirectAnySeatState(t *testing.T) {
	setups := bashSeatStateSetups()
	allowed := []string{
		`make test > "/tmp/sty_e642df17/maketest.log" 2>&1`,
		`cmd > "/tmp/sty_e642df17/x.log"`,
		`cmd > '/tmp/sty_e642df17/x.log'`,
		`cmd >"/tmp/sty_e642df17/x.log" 2>>"/tmp/sty_e642df17/err.log"`,
		`cmd &> /tmp/sty_e642df17/x.log`,
		`cmd > /tmp/sty_e642df17/x.log 2>&1`,
		"cat >> '/tmp/sty_e642df17/out.md' <<'EOF'\nbody\nEOF",
	}
	denied := []string{
		`echo x > internal/foo.go`,
		`echo x > "README.md" 2>&1`,
		`echo x > './README.md'`,
		`echo x > "$UNSET_VAR_FOR_TEST/x.log"`,
		`echo x > "$(pwd)/README.md"`,
		// A variable assigned earlier in the same command is not resolved: gated.
		"F=/tmp/sty_e642df17/out.md; cat >> $F <<'EOF'\nbody\nEOF",
	}
	checkBashWritesAnySeatState(t, setups, allowed, denied)
}

// checkBashWritesAnySeatState runs each command through both Bash hooks under
// every setup: the allowed ones must pass, the denied ones must be refused with
// that setup's reason.
func checkBashWritesAnySeatState(t *testing.T, setups []bashSeatState, allowed, denied []string) {
	t.Helper()
	for _, s := range setups {
		t.Run(s.name, func(t *testing.T) {
			s.setup(t)
			for _, c := range allowed {
				for _, sub := range []string{"gate", "commitgate"} {
					if out, err := runRootIn(t, sessionBBashEvent(c), "hook", sub); err != nil {
						t.Errorf("hook %s denied scratch write %q: %v\n%s", sub, c, err, out)
					}
				}
			}
			for _, c := range denied {
				for _, sub := range []string{"gate", "commitgate"} {
					out, err := runRootIn(t, sessionBBashEvent(c), "hook", sub)
					if err == nil {
						t.Errorf("hook %s allowed repo write %q:\n%s", sub, c, out)
						continue
					}
					if !denyReasonContains(out, s.wantDeny) {
						t.Errorf("hook %s deny of %q lacks %q:\n%s", sub, c, s.wantDeny, out)
					}
				}
			}
		})
	}
}

// TestBashWrappedQuotedTeeAnySeatState (sty_e79be13a): a quoted tee destination
// gets the verdict of the bare one when tee runs behind a wrapper command — refused
// into the repo tree with that seat state's reason, passed into an exempt location
// whatever the seat state.
func TestBashWrappedQuotedTeeAnySeatState(t *testing.T) {
	denied := []string{
		`true | env tee README.md`, // unquoted control
		`true | env tee "README.md"`,
		`sudo tee 'internal/x.go'`,
		`nohup tee -a "README.md"`,
		`xargs tee "README.md"`,
		`stdbuf -o0 tee "README.md"`,
		`echo x | tee "README.md"`,
	}
	allowed := []string{
		`true | env tee "/tmp/sty_e79be13a/x.log"`,
		`sudo tee '/tmp/sty_e79be13a/x.log'`,
		`nohup tee -a "/tmp/sty_e79be13a/x.log"`,
		`xargs tee "/tmp/sty_e79be13a/x.log"`,
		`stdbuf -o0 tee "/tmp/sty_e79be13a/x.log"`,
		`echo x | tee "/tmp/sty_e79be13a/x.log" > /dev/null`,
	}
	checkBashWritesAnySeatState(t, bashSeatStateSetups(), allowed, denied)
}
