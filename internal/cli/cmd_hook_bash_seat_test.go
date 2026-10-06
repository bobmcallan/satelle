package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
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
